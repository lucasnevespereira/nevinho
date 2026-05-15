// Package tui is the local terminal client for nevinho. It drives the same
// agent core the Discord transport does, rendered in the terminal.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/llm"
)

// userID namespaces this session's history in the agent.
const userID = "cli-local"

// inputPlaceholder is the textarea's resting placeholder text.
const inputPlaceholder = "Ask nevinho anything…"

var (
	colAccent = lipgloss.Color("12")
	colDim    = lipgloss.Color("244")
	colErr    = lipgloss.Color("9")
	colWarn   = lipgloss.Color("214")

	styleHint      = lipgloss.NewStyle().Foreground(colDim)
	styleErr       = lipgloss.NewStyle().Foreground(colErr)
	styleInput     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim)
	styleApprove   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colWarn)
	styleApproveLn = lipgloss.NewStyle().Foreground(colWarn)
	styleStatus    = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236"))
	styleSpin      = lipgloss.NewStyle().Foreground(colAccent)
	styleSelected  = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleUserMark  = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	// Agent text is the model's voice — soft and italic, like pi's
	// "thinking". Tool cards and approvals stay bright, so the visual
	// hierarchy is: bright = facts, dim italic = the model talking.
	styleAgent     = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	styleToolHead  = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleCard      = lipgloss.NewStyle().Background(lipgloss.Color("234")).Foreground(lipgloss.Color("250")).Padding(1, 2)
	styleCardErr   = lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("252")).Padding(1, 2)

	// Fg-only diff colours, like git diff: a calm green/red on the +/- lines
	// rather than a loud full-line background.
	styleDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleDiffHunk = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleDiffMeta = lipgloss.NewStyle().Foreground(colDim)
)

// Run starts the terminal UI and blocks until the user quits. cwd shows in
// the status bar; configDir is where the agent's log file is written.
func Run(a *agent.Agent, cwd, configDir string) error {
	// The agent logs through the stdlib logger, which writes to stderr and
	// would shred the alt screen. Send it to a file so the terminal stays
	// clean; tail ~/.nevinho/chat.log to see it.
	if f, err := tea.LogToFile(filepath.Join(configDir, "chat.log"), ""); err == nil {
		defer f.Close()
	}

	// Buffered so the agent's tool callback never blocks on a slow UI; a
	// full buffer just drops an event, which only costs a missed card.
	events := make(chan agent.ToolEvent, 64)
	a.SetToolCallback(userID, func(ev agent.ToolEvent) {
		select {
		case events <- ev:
		default:
		}
	})
	defer a.SetToolCallback(userID, nil)

	p := tea.NewProgram(newModel(a, events, cwd),
		tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// responseMsg carries the result of one finished agent turn.
type responseMsg struct {
	text string
	err  error
}

// toolEventMsg is one tool-call event lifted from the agent's callback
// channel into the Bubble Tea message loop.
type toolEventMsg agent.ToolEvent

type model struct {
	agent  *agent.Agent
	events chan agent.ToolEvent
	cwd    string

	vp    viewport.Model
	input textarea.Model
	spin  spinner.Model

	blocks         []block
	busy           bool
	expanded       bool // ctrl+o: show full tool output instead of previews
	selecting      bool // a picker (model or config) is open
	approving      bool // a tool action is awaiting yes/no
	approvalCursor int  // 0 = yes, 1 = no
	sel            selector
	selKind        string // "model" or "config": what the open picker resolves to
	configKey      string // non-empty while the input captures a value for this key
	width          int
	height         int
	ready          bool
}

func newModel(a *agent.Agent, events chan agent.ToolEvent, cwd string) model {
	ta := textarea.New()
	ta.Placeholder = inputPlaceholder
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()
	// Drop the default cursor-line background; it reads as an ugly block.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleSpin

	greeting := hintBlock{"nevinho · " + a.Model() + "\ntype a message · /help for commands · ctrl+c to quit"}
	if len(a.AvailableModels()) == 0 {
		greeting = hintBlock{"nevinho — no LLM provider configured yet\ntype /config to add one, then /model to pick a model"}
	}

	return model{
		agent:  a,
		events: events,
		cwd:    cwd,
		input:  ta,
		spin:   sp,
		blocks: []block{greeting},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.listen())
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

// send runs one blocking agent turn off the UI goroutine.
func (m model) send(text string) tea.Cmd {
	return func() tea.Msg {
		out, err := m.agent.Chat(userID, text, false, nil)
		return responseMsg{text: out, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		if m.selecting {
			return m.updateSelector(msg.String())
		}
		if m.approving {
			return m.updateApproval(msg.String())
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		case "ctrl+o":
			m.toggleExpand()
			return m, nil
		case "esc":
			if m.configKey != "" {
				m.configKey = ""
				m.input.Reset()
				m.input.Placeholder = inputPlaceholder
				m.add(hintBlock{"cancelled"})
			}
			return m, nil
		case "enter":
			return m.submit()
		}

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case responseMsg:
		m.busy = false
		switch {
		case msg.err != nil:
			m.add(errorBlock{llm.FriendlyError(msg.err)})
		case m.agent.HasPendingApproval(userID):
			// The reply is the agent asking permission. The picker at the
			// bottom resolves the decision; the message block has no keys.
			m.add(approvalBlock{msg.text})
			m.approving = true
			m.approvalCursor = 0
		default:
			m.add(agentBlock{msg.text})
		}
		return m, nil

	case toolEventMsg:
		ev := agent.ToolEvent(msg)
		// A card is the result of a finished tool. NEEDS_APPROVAL is the
		// approval handshake, not a real result, so skip it; the agent's
		// reply carries the approval prompt instead.
		if ev.Phase == agent.ToolDone && !strings.HasPrefix(ev.Output, "NEEDS_APPROVAL:") {
			m.add(toolBlock{name: ev.Name, detail: ev.Detail, input: ev.Input, output: ev.Output, isError: ev.IsError, expanded: m.expanded})
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
	return m, cmd
}

// updateSelector handles one keypress while a picker is open. A non-empty
// chosen with open=true is a toggle (config booleans); open=false is a pick.
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
			m.toggleConfig(chosen)
		}
		return m, nil
	}
	// Pick action.
	m.selecting = false
	switch m.selKind {
	case "model":
		if chosen != m.agent.Model() {
			if err := m.agent.SwitchModel(chosen); err != nil {
				m.add(errorBlock{err.Error()})
			} else {
				m.add(hintBlock{"switched to " + chosen})
			}
		}
	case "config":
		// chosen is a config key name; capture its value next.
		m.configKey = chosen
		m.input.Reset()
		m.input.Placeholder = "value for " + configLabels[chosen]
	}
	return m, nil
}

// toggleConfig flips a boolean config key (CAVEMAN, ELEPHANT) and refreshes
// the open selector's items so the ✓ moves immediately.
func (m *model) toggleConfig(key string) {
	next := "on"
	if m.agent.GetConfig(key) == "on" {
		next = "off"
	}
	if err := m.agent.SetConfig(key, next); err != nil {
		m.add(errorBlock{err.Error()})
		return
	}
	m.sel.items = configItems(m.agent.ConfigKeys(), m.agent.GetConfig)
}

// updateApproval handles one keypress while a tool action awaits a yes/no
// decision. Arrow keys move the cursor between yes and no, enter chooses,
// and y/n/esc are shortcuts. The decision routes through the agent's
// existing approval text protocol.
func (m model) updateApproval(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		return m, tea.Quit
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
	return m, tea.Batch(m.send(answer), m.spin.Tick)
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
			m.add(hintBlock{"cancelled"})
			return m, nil
		}
		if err := m.agent.SetConfig(key, text); err != nil {
			m.add(errorBlock{err.Error()})
		} else {
			m.add(hintBlock{"set " + key})
		}
		return m, nil
	}

	if text == "" {
		return m, nil
	}
	m.input.Reset()
	if cmd, handled := m.handleSlash(text); handled {
		return m, cmd
	}
	m.add(userBlock{text})
	m.busy = true
	return m, tea.Batch(m.send(text), m.spin.Tick)
}

// slashCommands is the canonical list, used for the /help text and the
// type-ahead hint shown while the input starts with "/".
var slashCommands = []struct{ name, desc string }{
	{"/model", "list models, or switch to one"},
	{"/config", "view config, set a key, or clear it"},
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
		m.add(hintBlock{"history cleared"})
		return nil, true
	case "/model":
		m.handleModel(arg)
		return nil, true
	case "/config":
		m.handleConfig(arg)
		return nil, true
	case "/help":
		m.add(hintBlock{commandHelp()})
		return nil, true
	}
	// An unknown slash command is a typo, not a message — keep it out of
	// the agent and point at /help.
	if strings.HasPrefix(cmd, "/") {
		m.add(hintBlock{"unknown command " + cmd + " — /help for the list"})
		return nil, true
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

// handleConfig lists config when called bare, sets a key when given a key
// and value, or clears a key when given just a key.
func (m *model) handleConfig(arg string) {
	if arg == "" {
		m.sel = newSelector("configure", configItems(m.agent.ConfigKeys(), m.agent.GetConfig))
		m.selKind = "config"
		m.selecting = true
		return
	}
	// /config KEY value — the direct path for power users.
	key, value, _ := strings.Cut(arg, " ")
	key = strings.ToUpper(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if err := m.agent.SetConfig(key, value); err != nil {
		m.add(errorBlock{err.Error()})
		return
	}
	if value == "" {
		m.add(hintBlock{"cleared " + key})
	} else {
		m.add(hintBlock{"set " + key})
	}
}

// handleModel opens the picker when called bare, or switches directly to
// the named model.
func (m *model) handleModel(name string) {
	if name == "" {
		models := m.agent.AvailableModels()
		if len(models) == 0 {
			m.add(hintBlock{"no models available — type /config to add a provider"})
			return
		}
		m.sel = newSelector("pick a model", modelItems(models, m.agent.Model()))
		m.selKind = "model"
		m.selecting = true
		return
	}
	if err := m.agent.SwitchModel(name); err != nil {
		m.add(errorBlock{err.Error()})
		return
	}
	m.add(hintBlock{"switched to " + name})
}

func (m model) View() string {
	if !m.ready {
		return "starting nevinho…"
	}
	if m.selecting {
		body := lipgloss.NewStyle().Height(m.height - 1).Render(m.sel.view())
		return body + "\n" + m.statusBar()
	}
	var bottom string
	if m.approving {
		bottom = m.approvalPicker()
	} else {
		bottom = styleInput.Width(m.width - 2).Render(m.input.View())
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.vp.View(),
		m.workingLine(),
		bottom,
		m.statusBar(),
	)
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
	return styleApprove.Width(m.width - 2).Render(line)
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
		left += fmt.Sprintf("  ·  ↑%s ↓%s  $%.3f", humanCount(in), humanCount(out), cost)
	}
	right := m.agent.Model() + " "
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	bar := left + strings.Repeat(" ", gap) + right
	return styleStatus.Width(m.width).Render(bar)
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

// layout sizes the viewport and input to the current terminal size. It
// reserves a row for the working line so the layout is stable busy or idle.
func (m *model) layout() {
	workingH := 1
	inputH := m.input.Height() + 2 // textarea plus its rounded border
	statusH := 1
	vpH := max(m.height-workingH-inputH-statusH, 1)

	m.vp = viewport.New(m.width, vpH)
	m.input.SetWidth(m.width - 2) // inside the border
	m.setContent()
}

// add appends a block to the transcript and scrolls to it.
func (m *model) add(b block) {
	m.blocks = append(m.blocks, b)
	m.setContent()
}

// toggleExpand flips every tool card between its preview and full output.
func (m *model) toggleExpand() {
	m.expanded = !m.expanded
	for i, b := range m.blocks {
		if tb, ok := b.(toolBlock); ok {
			tb.expanded = m.expanded
			m.blocks[i] = tb
		}
	}
	m.setContent()
}

// setContent re-renders every block to the viewport width and scrolls down.
// The viewport does not soft-wrap, so each block wraps itself here.
func (m *model) setContent() {
	if m.vp.Width == 0 {
		return
	}
	parts := make([]string, len(m.blocks))
	for i, b := range m.blocks {
		parts[i] = b.render(m.vp.Width)
	}
	m.vp.SetContent(strings.Join(parts, "\n\n"))
	m.vp.GotoBottom()
}

// shortenHome swaps the home directory prefix for ~ so the status bar stays short.
func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
