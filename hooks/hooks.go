// Package hooks runs optional user commands for local terminal lifecycle events.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	TurnStarted       = "turn_started"
	ApprovalRequested = "approval_requested"
	TurnFinished      = "turn_finished"
	ToolStarted       = "tool_started"
	ToolFinished      = "tool_finished"
	SessionExited     = "session_exited"

	commandTimeout = 5 * time.Second
	queueSize      = 256
)

var validEvents = map[string]bool{
	TurnStarted: true, ApprovalRequested: true, TurnFinished: true,
	ToolStarted: true, ToolFinished: true, SessionExited: true,
}

// Event is the version 1 hook payload. Prompt text and tool input/output are
// intentionally absent so enabling a hook does not disclose their contents.
type Event struct {
	Event     string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Outcome   string    `json:"outcome,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Status    string    `json:"status,omitempty"`
}

// Config maps event names to commands. Each command is an argv array; a shell
// runs only when the user explicitly configures one (for example, sh -c).
type Config map[string][][]string

// Load reads hooks.json in configDir. A missing file disables hooks.
func Load(configDir string) (*Runner, error) {
	path := filepath.Join(configDir, "hooks.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(nil), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for event, commands := range cfg {
		if !validEvents[event] {
			return nil, fmt.Errorf("%s: unknown event %q", path, event)
		}
		for i, argv := range commands {
			if len(argv) == 0 || argv[0] == "" {
				return nil, fmt.Errorf("%s: %s command %d needs an executable", path, event, i+1)
			}
		}
	}
	return New(cfg), nil
}

// Runner dispatches commands in event order without blocking the terminal UI.
type Runner struct {
	config Config
	events chan Event
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
}

func New(cfg Config) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{config: cfg, events: make(chan Event, queueSize), done: make(chan struct{}), ctx: ctx, cancel: cancel}
	go r.run()
	return r
}

// Emit queues an event when it has configured commands. If a slow hook fills
// the queue, the event is dropped and logged rather than stalling the TUI.
func (r *Runner) Emit(event Event) {
	if r == nil || len(r.config[event.Event]) == 0 {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	select {
	case r.events <- event:
	default:
		log.Printf("hook %s: event queue full; event dropped", event.Event)
	}
}

func (r *Runner) run() {
	defer close(r.done)
	for event := range r.events {
		if r.ctx.Err() != nil {
			return
		}
		r.execute(r.ctx, event)
	}
}

func (r *Runner) execute(parent context.Context, event Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("hook %s: encode event: %v", event.Event, err)
		return
	}
	for _, argv := range r.config[event.Event] {
		if parent.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(parent, commandTimeout)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = bytes.NewReader(append(payload, '\n'))
		err := cmd.Run()
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("hook %s (%s): timed out", event.Event, argv[0])
			} else {
				log.Printf("hook %s (%s): %v", event.Event, argv[0], err)
			}
		}
		cancel()
	}
}

// Close drains queued events and then runs session_exited. This is called
// after the UI has stopped, so a bounded wait cannot stall rendering.
func (r *Runner) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.events)
	r.mu.Unlock()
	select {
	case <-r.done:
	case <-time.After(commandTimeout):
		log.Print("hook queue: stopping slow or queued events on exit")
		r.cancel()
		<-r.done
	}
	r.cancel()
	if len(r.config[SessionExited]) > 0 {
		r.execute(context.Background(), Event{Event: SessionExited, Timestamp: time.Now().UTC()})
	}
}
