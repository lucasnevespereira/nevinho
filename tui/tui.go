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

// cardPreviewLines caps how many lines of tool output a card shows.
const cardPreviewLines = 12

var (
	colAccent = lipgloss.Color("12")
	colDim    = lipgloss.Color("244")
	colErr    = lipgloss.Color("9")

	styleHint     = lipgloss.NewStyle().Foreground(colDim)
	styleErr      = lipgloss.NewStyle().Foreground(colErr)
	styleInput    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim)
	styleStatus   = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236"))
	styleSpin     = lipgloss.NewStyle().Foreground(colAccent)
	styleUser     = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("252")).Padding(0, 1)
	styleToolHead = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleCard     = lipgloss.NewStyle().Background(lipgloss.Color("234")).Foreground(lipgloss.Color("250")).Padding(0, 1)
	styleCardErr  = lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("252")).Padding(0, 1)
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

// block is one rendered unit of the transcript. Each renders itself to the
// given width, so a terminal resize just re-renders every block.
type block interface {
	render(width int) string
}

// userBlock is a message the user sent: a full-width tinted bar, no label.
type userBlock struct{ text string }

func (b userBlock) render(w int) string {
	return styleUser.Width(w).Render(b.text)
}

// agentBlock is the agent's reply: plain wrapped text.
type agentBlock struct{ text string }

func (b agentBlock) render(w int) string {
	return lipgloss.NewStyle().Width(w).Render(strings.TrimRight(b.text, "\n"))
}

// hintBlock is dim helper text (the greeting, slash-command output).
type hintBlock struct{ text string }

func (b hintBlock) render(w int) string {
	return styleHint.Width(w).Render(b.text)
}

// errorBlock is a failed turn, already run through llm.FriendlyError.
type errorBlock struct{ msg string }

func (b errorBlock) render(w int) string {
	return styleErr.Width(w).Render("⚠ " + b.msg)
}

// toolBlock is a finished tool call: a header line plus a boxed preview of
// the output, tinted red when the tool errored.
type toolBlock struct {
	name, detail, output string
	isError              bool
}

func (b toolBlock) render(w int) string {
	card := styleCard
	if b.isError {
		card = styleCardErr
	}
	return toolHeader(b.name, b.detail) + "\n" + card.Width(w).Render(toolPreview(b.output))
}

// toolHeader renders the one-line title of a tool card: a verb plus its
// target. bash shows the command itself.
func toolHeader(name, detail string) string {
	if name == "bash" {
		return styleToolHead.Render("$ ") + styleHint.Render(detail)
	}
	verb := name
	switch name {
	case "file_read":
		verb = "read"
	case "file_write":
		verb = "write"
	case "file_edit":
		verb = "edit"
	case "file_list":
		verb = "list"
	case "web_search":
		verb = "search"
	case "web_read":
		verb = "fetch"
	}
	head := styleToolHead.Render(verb)
	if detail != "" {
		head += " " + styleHint.Render(detail)
	}
	return head
}

// toolPreview trims tool output to a previewable size with a "more" hint.
func toolPreview(output string) string {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return styleHint.Render("(no output)")
	}
	lines := strings.Split(output, "\n")
	if len(lines) <= cardPreviewLines {
		return output
	}
	shown := strings.Join(lines[:cardPreviewLines], "\n")
	return shown + "\n" + styleHint.Render(fmt.Sprintf("… %d more lines", len(lines)-cardPreviewLines))
}

type model struct {
	agent  *agent.Agent
	events chan agent.ToolEvent
	cwd    string

	vp    viewport.Model
	input textarea.Model
	spin  spinner.Model

	blocks []block
	busy   bool
	width  int
	height int
	ready  bool
}

func newModel(a *agent.Agent, events chan agent.ToolEvent, cwd string) model {
	ta := textarea.New()
	ta.Placeholder = "Ask nevinho anything…"
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
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		case "enter":
			return m.submit()
		}

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case responseMsg:
		m.busy = false
		if msg.err != nil {
			m.add(errorBlock{llm.FriendlyError(msg.err)})
		} else {
			m.add(agentBlock{msg.text})
		}
		return m, nil

	case toolEventMsg:
		ev := agent.ToolEvent(msg)
		// A card is the result of a finished tool. NEEDS_APPROVAL is the
		// approval handshake, not a real result, so skip it; the agent's
		// reply carries the approval prompt instead.
		if ev.Phase == agent.ToolDone && !strings.HasPrefix(ev.Output, "NEEDS_APPROVAL:") {
			m.add(toolBlock{name: ev.Name, detail: ev.Detail, output: ev.Output, isError: ev.IsError})
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

// submit sends the current input to the agent, or runs it as a slash command.
func (m model) submit() (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	text := strings.TrimSpace(m.input.Value())
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

// handleSlash runs the in-TUI slash commands. The bool reports whether the
// input was a slash command and should not go to the agent.
func (m *model) handleSlash(text string) (tea.Cmd, bool) {
	switch text {
	case "/quit", "/q":
		return tea.Quit, true
	case "/forget":
		m.agent.ClearHistory(userID)
		m.add(hintBlock{"history cleared"})
		return nil, true
	case "/help":
		m.add(hintBlock{"/forget  wipe this session's history\n/quit    leave (or ctrl+c)\n/help    this"})
		return nil, true
	}
	return nil, false
}

func (m model) View() string {
	if !m.ready {
		return "starting nevinho…"
	}
	input := styleInput.Width(m.width - 2).Render(m.input.View())
	return lipgloss.JoinVertical(lipgloss.Left,
		m.vp.View(),
		m.workingLine(),
		input,
		m.statusBar(),
	)
}

// workingLine is the spinner row shown just above the input while a turn
// runs. It always occupies one row so the layout never jumps.
func (m model) workingLine() string {
	if !m.busy {
		return ""
	}
	return " " + m.spin.View() + " " + styleHint.Render("working…")
}

// statusBar renders the bottom bar: working directory on the left, the
// active model on the right.
func (m model) statusBar() string {
	left := " " + shortenHome(m.cwd)
	right := m.agent.Model() + " "
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	bar := left + strings.Repeat(" ", gap) + right
	return styleStatus.Width(m.width).Render(bar)
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
