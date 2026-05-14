// Package tui is the local terminal client for nevinho. It drives the same
// agent core the Discord transport does, rendered in the terminal.
package tui

import (
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

var (
	colAccent = lipgloss.Color("12")
	colDim    = lipgloss.Color("244")
	colErr    = lipgloss.Color("9")
	colBar    = lipgloss.Color("236")
	colBarFg  = lipgloss.Color("250")

	styleYou    = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleTool   = lipgloss.NewStyle().Foreground(colDim)
	styleErr    = lipgloss.NewStyle().Foreground(colErr)
	styleHint   = lipgloss.NewStyle().Foreground(colDim)
	styleInput  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim)
	styleStatus = lipgloss.NewStyle().Foreground(colBarFg).Background(colBar)
	styleSpin   = lipgloss.NewStyle().Foreground(colAccent)
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
	// full buffer just drops an event, which only costs a missed line.
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

	transcript string // raw, re-wrapped to width on render
	busy       bool
	width      int
	height     int
	ready      bool
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

	greeting := styleHint.Render("nevinho · "+a.Model()) + "\n" +
		styleHint.Render("type a message · /help for commands · ctrl+c to quit") + "\n\n"

	return model{
		agent:      a,
		events:     events,
		cwd:        cwd,
		input:      ta,
		spin:       sp,
		transcript: greeting,
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
			m.addError(msg.err)
		} else {
			m.addAgent(msg.text)
		}
		return m, nil

	case toolEventMsg:
		m.addTool(agent.ToolEvent(msg))
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
	m.addUser(text)
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
		m.addHint("history cleared")
		return nil, true
	case "/help":
		m.addHint("/forget  wipe this session's history\n/quit    leave (or ctrl+c)\n/help    this")
		return nil, true
	}
	return nil, false
}

func (m model) View() string {
	if !m.ready {
		return "starting nevinho…"
	}
	input := styleInput.Width(m.width - 2).Render(m.input.View())
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), input, m.statusBar())
}

// statusBar renders the bottom bar: working directory on the left, the
// active model on the right, a spinner in the middle while a turn runs.
func (m model) statusBar() string {
	left := " " + shortenHome(m.cwd)
	right := m.agent.Model() + " "
	mid := ""
	if m.busy {
		mid = m.spin.View() + " working…"
	}
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(mid)-lipgloss.Width(right), 2)
	bar := left + strings.Repeat(" ", gap/2) + mid + strings.Repeat(" ", gap-gap/2) + right
	return styleStatus.Width(m.width).Render(bar)
}

// layout sizes the viewport and input to the current terminal size.
func (m *model) layout() {
	inputH := m.input.Height() + 2 // textarea plus its rounded border
	statusH := 1
	vpH := max(m.height-inputH-statusH, 1)

	m.vp = viewport.New(m.width, vpH)
	m.input.SetWidth(m.width - 2) // inside the border
	m.setContent()
}

// setContent re-wraps the transcript to the viewport width and scrolls down.
// The viewport does not soft-wrap, so wrapping happens here.
func (m *model) setContent() {
	if m.vp.Width == 0 {
		return
	}
	wrapped := lipgloss.NewStyle().Width(m.vp.Width).Render(m.transcript)
	m.vp.SetContent(wrapped)
	m.vp.GotoBottom()
}

func (m *model) addUser(text string) {
	m.transcript += styleYou.Render("› you") + "\n" + text + "\n\n"
	m.setContent()
}

func (m *model) addAgent(text string) {
	m.transcript += strings.TrimRight(text, "\n") + "\n\n"
	m.setContent()
}

func (m *model) addTool(ev agent.ToolEvent) {
	line := "  ⋮ " + ev.Name
	if ev.Detail != "" {
		line += " " + ev.Detail
	}
	m.transcript += styleTool.Render(line) + "\n"
	m.setContent()
}

func (m *model) addError(err error) {
	m.transcript += styleErr.Render("⚠ "+llm.FriendlyError(err)) + "\n\n"
	m.setContent()
}

func (m *model) addHint(s string) {
	m.transcript += styleHint.Render(s) + "\n\n"
	m.setContent()
}

// shortenHome swaps the home directory prefix for ~ so the status bar stays short.
func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
