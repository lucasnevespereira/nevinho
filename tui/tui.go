// Package tui is the local terminal client for nevinho. It drives the same
// agent core the Discord transport does, just rendered in the terminal.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lucasnevespereira/nevinho/agent"
)

// userID namespaces this session's history in the agent. It matches the
// plain REPL so a user dropping between the two shares one conversation.
const userID = "cli-local"

var (
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Background(lipgloss.Color("236"))
)

// Run starts the terminal UI against the agent and blocks until the user
// quits. cwd is shown in the status bar.
func Run(a *agent.Agent, cwd string) error {
	// Buffered so the agent's tool callback never blocks on a slow UI; a
	// full buffer just drops an event, which only costs a missed log line.
	events := make(chan agent.ToolEvent, 64)
	a.SetToolCallback(userID, func(ev agent.ToolEvent) {
		select {
		case events <- ev:
		default:
		}
	})
	defer a.SetToolCallback(userID, nil)

	p := tea.NewProgram(newModel(a, events, cwd), tea.WithAltScreen())
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

	transcript string
	busy       bool
	width      int
	height     int
	ready      bool
}

func newModel(a *agent.Agent, events chan agent.ToolEvent, cwd string) model {
	ta := textarea.New()
	ta.Placeholder = "Message nevinho..."
	ta.Prompt = "▌ "
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		agent:  a,
		events: events,
		cwd:    cwd,
		input:  ta,
		spin:   sp,
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
		case "enter":
			if m.busy {
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if cmd, handled := m.handleSlash(text); handled {
				m.input.Reset()
				return m, cmd
			}
			m.addBlock(userStyle.Render("you") + "\n" + text + "\n\n")
			m.input.Reset()
			m.busy = true
			return m, tea.Batch(m.send(text), m.spin.Tick)
		}

	case responseMsg:
		m.busy = false
		if msg.err != nil {
			m.addBlock(errStyle.Render("error: "+msg.err.Error()) + "\n\n")
		} else {
			m.addBlock(msg.text + "\n\n")
		}
		return m, nil

	case toolEventMsg:
		ev := agent.ToolEvent(msg)
		line := "  " + ev.Name
		if ev.Detail != "" {
			line += " " + ev.Detail
		}
		m.addBlock(toolStyle.Render(line) + "\n")
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

func (m model) View() string {
	if !m.ready {
		return "starting nevinho..."
	}
	sep := hintStyle.Render(strings.Repeat("─", m.width))
	return m.vp.View() + "\n" + sep + "\n" + m.input.View() + "\n" + m.statusBar()
}

// handleSlash runs the in-TUI slash commands. The bool reports whether the
// input was a slash command and should not go to the agent.
func (m *model) handleSlash(text string) (tea.Cmd, bool) {
	switch text {
	case "/quit", "/q":
		return tea.Quit, true
	case "/forget":
		m.agent.ClearHistory(userID)
		m.addBlock(hintStyle.Render("(history cleared)") + "\n\n")
		return nil, true
	case "/help":
		m.addBlock(hintStyle.Render("/quit leave  ·  /forget wipe history  ·  /help this  ·  ctrl+c quit") + "\n\n")
		return nil, true
	}
	return nil, false
}

// addBlock appends rendered text to the transcript and scrolls to it.
func (m *model) addBlock(s string) {
	m.transcript += s
	m.vp.SetContent(m.transcript)
	m.vp.GotoBottom()
}

// layout sizes the viewport and input to the current terminal size.
func (m *model) layout() {
	statusH := 1
	sepH := 1
	inputH := m.input.Height()
	vpH := m.height - inputH - sepH - statusH
	if vpH < 1 {
		vpH = 1
	}
	m.vp = viewport.New(m.width, vpH)
	m.input.SetWidth(m.width)
	m.vp.SetContent(m.transcript)
	m.vp.GotoBottom()
}

// statusBar renders the bottom bar: working directory on the left, the
// active model on the right, a spinner in the middle while a turn runs.
func (m model) statusBar() string {
	left := " " + m.cwd
	right := m.agent.Model() + " "
	mid := ""
	if m.busy {
		mid = m.spin.View() + " working"
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(mid) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	bar := left + strings.Repeat(" ", gap/2) + mid + strings.Repeat(" ", gap-gap/2) + right
	return statusStyle.Width(m.width).Render(bar)
}
