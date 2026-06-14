// Package tui is the local terminal client for nevinho. It drives the same
// agent core the Discord transport does, rendered in the terminal.
//
// The render model is inline, not alt-screen: conversation blocks are
// printed straight into the terminal's regular scrollback via tea.Println,
// so the terminal handles wheel scrolling, text selection, and URL
// clicking natively. Only the live region at the bottom (input, status,
// pickers) is managed by Bubble Tea.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/llm"
)

// userID namespaces this session's history in the agent.
const userID = "terminal"

// inputPlaceholder is the textarea's resting placeholder text.
const inputPlaceholder = "Ask nevinho anything…"

// Run starts the terminal UI and blocks until the user quits. cwd shows in
// the status bar. configDir is where the agent's log file is written.
func Run(a *agent.Agent, cwd, configDir string) error {
	// The agent logs through the stdlib logger, which writes to stderr.
	// Send it to a file so the terminal stays clean. tail ~/.nevinho/chat.log
	// to follow it live.
	if f, err := tea.LogToFile(filepath.Join(configDir, "chat.log"), ""); err == nil {
		defer f.Close()
	}

	// Buffered so the agent's tool callback never blocks on a slow UI. a
	// full buffer just drops an event, which only costs a missed card.
	events := make(chan agent.ToolEvent, 64)
	stream := make(chan string, 256)
	a.SetToolCallback(userID, func(ev agent.ToolEvent) {
		select {
		case events <- ev:
		default:
		}
	})
	defer a.SetToolCallback(userID, nil)

	// Inline rendering, not alt-screen. The terminal owns scrollback,
	// selection, and URL clicks. Bubble Tea only manages the bottom live
	// region (input, status, pickers).
	p := tea.NewProgram(newModel(a, events, stream, cwd))
	_, err := p.Run()
	return err
}

// responseMsg carries the result of one finished agent turn.
type responseMsg struct {
	text     string
	err      error
	duration time.Duration
}

// toolEventMsg is one tool-call event lifted from the agent's callback
// channel into the Bubble Tea message loop.
type toolEventMsg agent.ToolEvent

type streamDeltaMsg string

type model struct {
	agent  *agent.Agent
	events chan agent.ToolEvent
	stream chan string
	cwd    string

	input textarea.Model
	spin  spinner.Model

	busy           bool
	selecting      bool // a picker (model or config) is open
	approving      bool // a tool action is awaiting yes/no
	approvalCursor int  // 0 = yes, 1 = no
	sel            selector
	selKind        string // "model" or "config": what the open picker resolves to
	configKey      string // non-empty while the input captures a value for this key
	width          int
	height         int
	ready          bool
	liveResponse   string
	lastTurnTime   time.Duration

	// Submitted prompts, newest last. Up/down on an empty input walks
	// this list so the user can re-send or edit a recent turn.
	history    []string
	historyIdx int // -1 means "not browsing", otherwise an index into history

	// File-mention state. The picker renders above the input bar; the
	// user keeps typing into the textarea and the text after @ becomes
	// the filter. mentionPrefix is the input value at the moment @ was
	// pressed, so we can splice the chosen path back in.
	mentioning    bool
	mentionPrefix string
	mentionItems  []string
	mentionCursor int
}

func newModel(a *agent.Agent, events chan agent.ToolEvent, stream chan string, cwd string) model {
	ta := textarea.New()
	ta.Placeholder = inputPlaceholder
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()
	// Drop the default cursor-line background. It reads as an ugly block.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleSpin

	return model{
		agent:      a,
		events:     events,
		stream:     stream,
		cwd:        cwd,
		input:      ta,
		spin:       sp,
		historyIdx: -1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.listen(), m.listenStream())
}

// listen pulls the next tool event off the channel. It re-issues itself on
// every event, so exactly one listener runs for the life of the program.
func (m model) listen() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return nil
		}
		return toolEventMsg(ev)
	}
}

func (m model) listenStream() tea.Cmd {
	return func() tea.Msg {
		delta, ok := <-m.stream
		if !ok {
			return nil
		}
		return streamDeltaMsg(delta)
	}
}

// send runs one blocking agent turn off the UI goroutine.
func (m model) send(text string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		out, err := m.agent.ChatStream(userID, text, false, nil, func(delta string) {
			select {
			case m.stream <- delta:
			default:
			}
		})
		return responseMsg{text: out, err: err, duration: time.Since(start)}
	}
}

// contentWidth is the width passed to block renders. Blocks stretch the
// full terminal width, matching pi's edge-to-edge layout.
func (m model) contentWidth() int {
	return m.width
}

// printBlock returns a Cmd that pushes a rendered block into the terminal
// scrollback, above the live region. The leading blank row gives every
// block one row of breathing space, so user turns, agent replies, and
// tool cards do not touch each other (pi-style).
func (m model) printBlock(b block) tea.Cmd {
	return tea.Println("\n" + b.render(m.contentWidth()))
}

// greeting is the first hint block, shown once on startup. Lays out like
// pi's startup header: name and version, then a one-line key hint.
func (m model) greeting() block {
	if len(m.agent.AvailableModels()) == 0 {
		return hintBlock{"nevinho " + m.agent.Version() + "\nno LLM provider configured yet · /config to add one · /model to pick"}
	}
	return hintBlock{"nevinho " + m.agent.Version() + "\nesc interrupt · ctrl+c quit · / commands · /help for the list"}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(m.width)
		if !m.ready {
			m.ready = true
			// Print the greeting straight into scrollback. The live region
			// sits right under it, and as content arrives new blocks push
			// the greeting up naturally.
			return m, tea.Println(m.greeting().render(m.contentWidth()))
		}
		return m, nil

	case tea.KeyMsg:
		// ctrl+c quits from any mode. Each sub-handler ignores it otherwise,
		// which leaves the user stuck inside a picker.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.selecting {
			return m.updateSelector(msg.String())
		}
		if m.approving {
			return m.updateApproval(msg.String())
		}
		switch msg.String() {
		case "esc":
			// While a turn is running, esc interrupts the agent. The Chat
			// goroutine returns "Cancelled." which lands as a normal reply.
			if m.busy {
				m.agent.Cancel(userID)
				return m, nil
			}
			if m.mentioning {
				m.mentioning = false
				return m, nil
			}
			if m.configKey != "" {
				m.configKey = ""
				m.input.Reset()
				m.input.Placeholder = inputPlaceholder
				return m, m.printBlock(hintBlock{"cancelled"})
			}
			return m, nil
		case "enter":
			if m.mentioning {
				return m.acceptMention()
			}
			return m.submit()
		case "ctrl+j", "shift+enter":
			// Insert a newline at the end of the current value and grow
			// the textarea so the user can compose a multi-paragraph
			// prompt before pressing enter to send. Most terminals only
			// emit shift+enter when the kitty keyboard protocol is on,
			// so ctrl+j is the portable fallback.
			m.input.SetValue(m.input.Value() + "\n")
			m.input.CursorEnd()
			m.resizeInput()
			return m, nil
		case "@":
			// Flag mention mode but let @ flow into the textarea so the
			// user sees what they typed. The filter follows the @ in the
			// input value.
			if m.canMention() {
				m.mentioning = true
				m.mentionPrefix = m.input.Value()
				m.mentionItems = listProjectFiles(m.cwd)
				m.mentionCursor = 0
			}
		case "up":
			if m.mentioning {
				if m.mentionCursor > 0 {
					m.mentionCursor--
				}
				return m, nil
			}
			if m.historyPrev() {
				return m, nil
			}
		case "down":
			if m.mentioning {
				if m.mentionCursor < len(m.filteredMentions())-1 {
					m.mentionCursor++
				}
				return m, nil
			}
			if m.historyNext() {
				return m, nil
			}
		}

	case responseMsg:
		m.busy = false
		m.liveResponse = ""
		m.lastTurnTime = msg.duration
		switch {
		case msg.err != nil:
			return m, m.printBlock(errorBlock{llm.FriendlyError(msg.err)})
		case m.agent.HasPendingApproval(userID):
			// The reply is the agent asking permission. The picker at the
			// bottom resolves the yes/no decision. The message itself goes
			// to scrollback like any other block.
			m.approving = true
			m.approvalCursor = 0
			return m, m.printBlock(approvalBlock{msg.text})
		default:
			return m, m.printBlock(agentBlock{msg.text})
		}

	case streamDeltaMsg:
		if m.busy {
			m.liveResponse += string(msg)
		}
		return m, m.listenStream()

	case toolEventMsg:
		ev := agent.ToolEvent(msg)
		// A card is the result of a finished tool. NEEDS_APPROVAL is the
		// approval handshake, not a real result, so skip it. The agent's
		// reply carries the approval prompt instead.
		if ev.Phase == agent.ToolDone && !strings.HasPrefix(ev.Output, "NEEDS_APPROVAL:") {
			card := toolBlock{name: ev.Name, detail: ev.Detail, input: ev.Input, output: ev.Output, isError: ev.IsError}
			return m, tea.Batch(m.printBlock(card), m.listen())
		}
		return m, m.listen()

	case spinner.TickMsg:
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeInput()
	m.refreshMention()
	return m, cmd
}

// inputMaxRows caps how tall the input grows. Past this the textarea
// scrolls inside its own box rather than pushing the live region up.
const inputMaxRows = 6

// resizeInput keeps the textarea height in sync with its content. A
// single-line prompt stays compact, a paste or ctrl+j grows it up to
// inputMaxRows so the user sees what they typed.
func (m *model) resizeInput() {
	rows := min(max(strings.Count(m.input.Value(), "\n")+1, 1), inputMaxRows)
	m.input.SetHeight(rows)
}

// updateSelector handles one keypress while a picker is open. A non-empty
// chosen with open=true is a toggle (config booleans). open=false is a pick.
func (m model) updateSelector(key string) (tea.Model, tea.Cmd) {
	sel, chosen, open := m.sel.update(key)
	m.sel = sel
	if chosen == "" {
		if !open {
			m.selecting = false
		}
		return m, nil
	}
	if open {
		// Toggle action: flip the boolean and refresh items in place so
		// the cursor stays on the toggled row.
		if m.selKind == "config" {
			return m, m.toggleConfig(chosen)
		}
		return m, nil
	}
	// Pick action.
	m.selecting = false
	switch m.selKind {
	case "model":
		if chosen != m.agent.Model() {
			if err := m.agent.SwitchModel(chosen); err != nil {
				return m, m.printBlock(errorBlock{err.Error()})
			}
			return m, m.printBlock(hintBlock{"switched to " + chosen})
		}
	case "config":
		// chosen is a config key name. capture its value next.
		m.configKey = chosen
		m.input.Reset()
		m.input.Placeholder = "value for " + configLabels[chosen]
	case "paths":
		if m.agent.RevokePath(chosen) {
			return m, m.printBlock(hintBlock{"revoked " + chosen})
		}
		return m, m.printBlock(hintBlock{"could not revoke " + chosen})
	}
	return m, nil
}

// toggleConfig flips a boolean config key (CAVEMAN, ELEPHANT) and refreshes
// the open selector's items so the ✓ moves immediately.
func (m *model) toggleConfig(key string) tea.Cmd {
	next := "on"
	if m.agent.GetConfig(key) == "on" {
		next = "off"
	}
	if err := m.agent.SetConfig(key, next); err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	m.sel.items = configItems(m.agent.ConfigKeys(), m.agent.GetConfig)
	return nil
}

// updateApproval handles one keypress while a tool action awaits a yes/no
// decision. Arrow keys move the cursor between yes and no, enter chooses,
// and y/n/esc are shortcuts.
func (m model) updateApproval(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "left", "h", "up", "k":
		m.approvalCursor = 0
		return m, nil
	case "right", "l", "down", "j":
		m.approvalCursor = 1
		return m, nil
	case "enter":
		return m.decideApproval(m.approvalCursor == 0)
	case "y", "Y":
		return m.decideApproval(true)
	case "n", "N", "esc":
		return m.decideApproval(false)
	}
	return m, nil // ignore everything else while a decision is pending
}

// decideApproval exits approval mode and sends the chosen answer.
func (m model) decideApproval(approve bool) (tea.Model, tea.Cmd) {
	m.approving = false
	answer := "no"
	if approve {
		answer = "yes"
	}
	m.busy = true
	m.liveResponse = ""
	return m, tea.Batch(m.send(answer), m.spin.Tick)
}

// canMention reports whether typing @ should arm the file picker. Only
// triggers at a word boundary so paths can still contain a literal @ (an
// email-like token mid-word stays out of the picker).
func (m model) canMention() bool {
	if m.busy || m.mentioning || m.selecting || m.approving || m.configKey != "" {
		return false
	}
	v := m.input.Value()
	if v == "" {
		return true
	}
	last := v[len(v)-1]
	return last == ' ' || last == '\n' || last == '\t'
}

// refreshMention checks whether the mention picker still makes sense
// after each keystroke. If the user erased past @ or typed a space, the
// picker closes; otherwise the cursor is clamped to the filtered list.
func (m *model) refreshMention() {
	if !m.mentioning {
		return
	}
	v := m.input.Value()
	want := m.mentionPrefix + "@"
	if !strings.HasPrefix(v, want) {
		m.mentioning = false
		return
	}
	filter := v[len(want):]
	if strings.ContainsAny(filter, " \n\t") {
		m.mentioning = false
		return
	}
	items := m.filteredMentions()
	if m.mentionCursor >= len(items) {
		m.mentionCursor = max(len(items)-1, 0)
	}
}

// filteredMentions returns the project files matching the current
// filter (the text after @ in the input).
func (m model) filteredMentions() []string {
	v := m.input.Value()
	want := m.mentionPrefix + "@"
	if !strings.HasPrefix(v, want) {
		return m.mentionItems
	}
	filter := strings.ToLower(v[len(want):])
	if filter == "" {
		return m.mentionItems
	}
	out := make([]string, 0, len(m.mentionItems))
	for _, p := range m.mentionItems {
		if strings.Contains(strings.ToLower(p), filter) {
			out = append(out, p)
		}
	}
	return out
}

// acceptMention splices the picked path into the prompt where @ was
// typed, replacing the @filter span and adding a trailing space so the
// user can keep typing without gluing the next word onto the path.
func (m model) acceptMention() (tea.Model, tea.Cmd) {
	items := m.filteredMentions()
	if m.mentionCursor >= len(items) {
		m.mentioning = false
		return m, nil
	}
	m.input.SetValue(m.mentionPrefix + items[m.mentionCursor] + " ")
	m.input.CursorEnd()
	m.mentioning = false
	return m, nil
}

// historyMax caps how many prompts we keep in the up/down recall ring.
const historyMax = 100

// recordHistory appends a submitted prompt to the recall ring. Duplicates
// of the most recent entry are skipped so up-arrow lands on a new line
// instead of the same one twice in a row.
func (m *model) recordHistory(text string) {
	if n := len(m.history); n > 0 && m.history[n-1] == text {
		m.historyIdx = -1
		return
	}
	m.history = append(m.history, text)
	if len(m.history) > historyMax {
		m.history = m.history[len(m.history)-historyMax:]
	}
	m.historyIdx = -1
}

// historyPrev loads the previous submitted prompt into the input. Returns
// false when there is nothing earlier to load, so the caller can let the
// keypress fall through to the textarea (which moves the cursor up).
func (m *model) historyPrev() bool {
	if len(m.history) == 0 {
		return false
	}
	// Only intercept when the user is not mid-edit, so up-arrow inside a
	// long pasted prompt still moves the textarea cursor.
	if m.historyIdx == -1 && strings.TrimSpace(m.input.Value()) != "" {
		return false
	}
	next := m.historyIdx - 1
	if m.historyIdx == -1 {
		next = len(m.history) - 1
	}
	if next < 0 {
		return true // already at the oldest, swallow the keypress
	}
	m.historyIdx = next
	m.input.SetValue(m.history[next])
	m.input.CursorEnd()
	return true
}

// historyNext walks forward through recall toward the live empty input.
// At the newest entry one more press clears the input and exits recall.
func (m *model) historyNext() bool {
	if m.historyIdx == -1 {
		return false
	}
	next := m.historyIdx + 1
	if next >= len(m.history) {
		m.historyIdx = -1
		m.input.Reset()
		return true
	}
	m.historyIdx = next
	m.input.SetValue(m.history[next])
	m.input.CursorEnd()
	return true
}

// submit sends the current input to the agent, or runs it as a slash command.
func (m model) submit() (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	text := strings.TrimSpace(m.input.Value())

	// Capturing a value for a config key picked in the /config selector.
	if m.configKey != "" {
		key := m.configKey
		m.configKey = ""
		m.input.Reset()
		m.input.Placeholder = inputPlaceholder
		if text == "" {
			return m, m.printBlock(hintBlock{"cancelled"})
		}
		if err := m.agent.SetConfig(key, text); err != nil {
			return m, m.printBlock(errorBlock{err.Error()})
		}
		return m, m.printBlock(hintBlock{"set " + key})
	}

	if text == "" {
		return m, nil
	}
	m.recordHistory(text)
	m.input.Reset()
	if cmd, handled := m.handleSlash(text); handled {
		// Echo known commands so the transcript shows what the user
		// typed, same as a normal turn. Typos go straight to the hint
		// without a user bubble.
		if !isKnownSlash(text) {
			if cmd == nil {
				return m, nil
			}
			return m, cmd
		}
		echo := m.printBlock(userBlock{text})
		if cmd == nil {
			return m, echo
		}
		return m, tea.Sequence(echo, cmd)
	}
	m.busy = true
	m.liveResponse = ""
	return m, tea.Batch(
		m.printBlock(userBlock{text}),
		m.send(text),
		m.spin.Tick,
	)
}

// isKnownSlash reports whether the leading word of text matches one of
// the registered slash commands. Used to gate the user-bubble echo so
// typos do not leave a stray transcript entry.
func isKnownSlash(text string) bool {
	cmd, _, _ := strings.Cut(strings.TrimSpace(text), " ")
	if cmd == "/q" {
		return true
	}
	for _, c := range slashCommands {
		if cmd == c.name {
			return true
		}
	}
	return false
}

// slashCommands is the canonical list, used for the /help text and the
// type-ahead hint shown while the input starts with "/".
var slashCommands = []struct{ name, desc string }{
	{"/model", "pick a model, or /model <name> to switch directly"},
	{"/config", "open the config picker, or /config KEY value to set"},
	{"/memory", "show what nevinho remembers about you"},
	{"/session", "show the saved session summary"},
	{"/status", "model, tokens, cost, approved paths"},
	{"/paths", "pick an approved path to revoke, or /paths clear"},
	{"/forget", "wipe this session's history"},
	{"/help", "show this list"},
	{"/quit", "leave (or ctrl+c)"},
}

// handleSlash runs the in-TUI slash commands. The bool reports whether the
// input was a slash command and should not go to the agent.
func (m *model) handleSlash(text string) (tea.Cmd, bool) {
	cmd, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "/quit", "/q":
		return tea.Quit, true
	case "/forget":
		m.agent.ClearHistory(userID)
		return m.printBlock(hintBlock{"history cleared"}), true
	case "/model":
		return m.handleModel(arg), true
	case "/config":
		return m.handleConfig(arg), true
	case "/memory":
		return m.printBlock(agentBlock{m.agent.MemoryView()}), true
	case "/session":
		return m.printBlock(agentBlock{m.agent.SummaryView(userID)}), true
	case "/status":
		return m.printBlock(agentBlock{m.agent.Status()}), true
	case "/paths":
		return m.handlePaths(arg), true
	case "/help":
		return m.printBlock(hintBlock{commandHelp()}), true
	}
	// An unknown slash command is a typo, not a message. Keep it out of
	// the agent and point at /help.
	if strings.HasPrefix(cmd, "/") {
		return m.printBlock(hintBlock{"unknown command " + cmd + ". /help for the list"}), true
	}
	return nil, false
}

// commandHelp renders the slash-command list for /help.
func commandHelp() string {
	var b strings.Builder
	for _, c := range slashCommands {
		b.WriteString(c.name)
		b.WriteString(strings.Repeat(" ", 10-len(c.name)))
		b.WriteString(c.desc)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// matchCommands returns the slash commands whose name starts with the input
// word, for the type-ahead hint. Empty unless the input starts with "/".
func matchCommands(input string) string {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return ""
	}
	word, _, _ := strings.Cut(input, " ")
	var names []string
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, word) {
			names = append(names, c.name)
		}
	}
	return strings.Join(names, "  ")
}

// handleConfig opens the picker when called bare, sets a key when given a
// key and value, or clears a key when given just a key.
func (m *model) handleConfig(arg string) tea.Cmd {
	if arg == "" {
		m.sel = newSelector("configure", configItems(m.agent.ConfigKeys(), m.agent.GetConfig))
		m.selKind = "config"
		m.selecting = true
		return nil
	}
	key, value, _ := strings.Cut(arg, " ")
	key = strings.ToUpper(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if err := m.agent.SetConfig(key, value); err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	if value == "" {
		return m.printBlock(hintBlock{"cleared " + key})
	}
	return m.printBlock(hintBlock{"set " + key})
}

// handlePaths opens the approved-paths picker (enter on a row revokes it),
// or runs the "clear" subcommand to wipe them all.
func (m *model) handlePaths(arg string) tea.Cmd {
	switch arg {
	case "":
		paths := m.agent.ApprovedPaths()
		if len(paths) == 0 {
			return m.printBlock(hintBlock{"no approved paths yet"})
		}
		m.sel = newSelector("revoke a path", pathItems(paths))
		m.selKind = "paths"
		m.selecting = true
		return nil
	case "clear":
		m.agent.ClearApprovedPaths()
		return m.printBlock(hintBlock{"cleared all approved paths"})
	default:
		return m.printBlock(hintBlock{"unknown /paths option. try /paths or /paths clear"})
	}
}

// handleModel opens the picker when called bare, or switches directly to
// the named model.
func (m *model) handleModel(name string) tea.Cmd {
	if name == "" {
		models := m.agent.AvailableModels()
		if len(models) == 0 {
			return m.printBlock(hintBlock{"no models available. type /config to add a provider"})
		}
		m.sel = newSelector("pick a model", modelItems(models, m.agent.Model()))
		m.selKind = "model"
		m.selecting = true
		return nil
	}
	if err := m.agent.SwitchModel(name); err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	return m.printBlock(hintBlock{"switched to " + name})
}

func (m model) View() string {
	if !m.ready {
		return ""
	}
	if m.selecting {
		return lipgloss.JoinVertical(lipgloss.Left, m.sel.view(), m.statusBar())
	}
	var bottom string
	if m.approving {
		bottom = m.approvalPicker()
	} else {
		// Hairline bar spans the full terminal width like pi's input row.
		// resizeInput keeps the height honest after Reset/SetValue paths
		// that ran outside the keystroke loop.
		m.resizeInput()
		bottom = styleInput.Width(m.width).Render(m.input.View())
	}
	if m.mentioning {
		return lipgloss.JoinVertical(lipgloss.Left,
			"",
			m.mentionPickerView(),
			bottom,
			m.statusBar(),
		)
	}
	// Leading blank row keeps the live region from hugging the last
	// printed block in scrollback.
	parts := []string{""}
	if m.liveResponse != "" {
		parts = append(parts, agentBlock{m.liveResponse}.render(m.contentWidth()))
	}
	parts = append(parts, m.workingLine(), bottom, m.statusBar())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// mentionPickerRows caps how many file rows the @-picker shows at once.
const mentionPickerRows = 8

// mentionPickerView renders the inline file list shown above the input
// while the user is typing an @ mention. The selected row is accented;
// the others dim, with a "+ " bullet that reads like pi's picker.
func (m model) mentionPickerView() string {
	items := m.filteredMentions()
	if len(items) == 0 {
		return styleHint.Render("  no match")
	}
	start := 0
	if m.mentionCursor >= mentionPickerRows {
		start = m.mentionCursor - mentionPickerRows + 1
	}
	end := min(start+mentionPickerRows, len(items))
	var b strings.Builder
	for i := start; i < end; i++ {
		row := "+ " + items[i]
		if i == m.mentionCursor {
			b.WriteString(styleSelected.Render(row))
		} else {
			b.WriteString(styleHint.Render(row))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// approvalPicker renders the yes/no chooser shown in place of the input
// while a tool action is awaiting a decision. Single-line content so the
// height matches the input box and the layout does not jump.
func (m model) approvalPicker() string {
	yes, no := "yes", "no"
	if m.approvalCursor == 0 {
		yes = styleSelected.Render("→ yes")
		no = "  " + no
	} else {
		yes = "  " + yes
		no = styleSelected.Render("→ no")
	}
	hint := styleHint.Render("approve?")
	keys := styleHint.Render("(↑↓ enter · y / n · esc)")
	line := hint + "  " + yes + "    " + no + "    " + keys
	return styleApprove.Width(m.width).Render(line)
}

// workingLine is the one row above the input: the spinner while a turn
// runs, the slash-command hint while the input starts with "/", otherwise
// blank. It always occupies one row so the layout never jumps.
func (m model) workingLine() string {
	if m.busy {
		return " " + m.spin.View() + " " + styleHint.Render("working…")
	}
	if m.configKey != "" {
		return " " + styleHint.Render("setting "+configLabels[m.configKey]+" · enter to save · esc to cancel")
	}
	if hint := matchCommands(m.input.Value()); hint != "" {
		return " " + styleHint.Render(hint)
	}
	return ""
}

// statusBar renders the bottom bar: working directory and token usage on
// the left, the active model on the right.
func (m model) statusBar() string {
	left := " " + shortenHome(m.cwd)
	if in, out, cost := m.agent.Usage(); in > 0 || out > 0 {
		left += fmt.Sprintf("  ·  ↑%s ↓%s  $%.2f", humanCount(in), humanCount(out), cost)
	}
	if m.lastTurnTime > 0 {
		left += "  ·  last " + humanDuration(m.lastTurnTime)
	}
	right := m.agent.Model() + " "
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	bar := left + strings.Repeat(" ", gap) + right
	return styleStatus.Width(m.width).Render(bar)
}

// humanDuration formats a completed turn duration for the compact status bar.
func humanDuration(d time.Duration) string {
	d = d.Round(100 * time.Millisecond)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Truncate(time.Second).String()
}

// humanCount formats a token count compactly: 500, 2.8k, 30k.
func humanCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%dk", n/1000)
	}
}

// shortenHome swaps the home directory prefix for ~ so the status bar stays short.
func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
