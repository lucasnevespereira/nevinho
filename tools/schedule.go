package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/schedule"
)

type scheduleInput struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	Cron   string `json:"cron"`
	Prompt string `json:"prompt"`
}

func (r *Registry) scheduleTool(input json.RawMessage) string {
	var in scheduleInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	r.mu.Lock()
	store := r.schedules
	r.mu.Unlock()

	if store == nil {
		return "scheduling is not enabled in this process"
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "", "list":
		return formatScheduleList(store.All())
	case "create":
		s, err := store.Create(in.Name, in.Cron, in.Prompt)
		if err != nil {
			return "failed: " + err.Error()
		}
		return fmt.Sprintf("created %q. Next run: %s", s.Name, formatTime(s.NextRun))
	case "delete":
		ok, err := store.Delete(in.Name)
		if err != nil {
			return "failed: " + err.Error()
		}
		if !ok {
			return fmt.Sprintf("no schedule named %q", in.Name)
		}
		return fmt.Sprintf("deleted %q", in.Name)
	case "pause":
		s, err := store.SetEnabled(in.Name, false)
		if err != nil {
			return "failed: " + err.Error()
		}
		return fmt.Sprintf("paused %q", s.Name)
	case "resume":
		s, err := store.SetEnabled(in.Name, true)
		if err != nil {
			return "failed: " + err.Error()
		}
		return fmt.Sprintf("resumed %q. Next run: %s", s.Name, formatTime(s.NextRun))
	default:
		return fmt.Sprintf("unknown action %q. Use list, create, delete, pause, or resume.", in.Action)
	}
}

func formatScheduleList(list []schedule.Schedule) string {
	if len(list) == 0 {
		return "no schedules"
	}
	var sb strings.Builder
	for _, s := range list {
		state := "on"
		if !s.Enabled {
			state = "paused"
		}
		fmt.Fprintf(&sb, "%s [%s] %s\n  cron: %s\n  next: %s\n  prompt: %s\n",
			s.Name, state, s.ID, s.Cron, formatTime(s.NextRun), truncate(s.Prompt, 80))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04 MST")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
