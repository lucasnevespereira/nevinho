package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/schedule"
)

type scheduleInput struct {
	Action   string `json:"action"`
	Name     string `json:"name"`
	Cron     string `json:"cron"`
	Prompt   string `json:"prompt"`
	Timezone string `json:"timezone"`
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
		s, err := store.Create(in.Name, in.Cron, in.Prompt, in.Timezone)
		if err != nil {
			return "failed: " + err.Error()
		}
		return fmt.Sprintf("created %q. Next run: %s", s.Name, formatScheduledTime(s))
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
		return fmt.Sprintf("resumed %q. Next run: %s", s.Name, formatScheduledTime(s))
	case "logs":
		s, ok := store.Find(in.Name)
		if !ok {
			return fmt.Sprintf("no schedule named %q", in.Name)
		}
		return formatScheduleLogs(s)
	default:
		return fmt.Sprintf("unknown action %q. Use list, create, delete, pause, resume, or logs.", in.Action)
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
		fmt.Fprintf(&sb, "%s [%s] %s\n  cron: %s%s\n  next: %s\n  prompt: %s\n",
			s.Name, state, s.ID,
			s.Cron, tzSuffix(s.Timezone),
			formatScheduledTime(s),
			truncate(s.Prompt, 80),
		)
		if last := lastRun(s); last != nil {
			fmt.Fprintf(&sb, "  last: %s\n", formatLastRun(*last))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatScheduledTime renders NextRun in the schedule's timezone for the
// agent's text reply.
func formatScheduledTime(s schedule.Schedule) string {
	if s.NextRun.IsZero() {
		return "never"
	}
	return s.NextRunIn().Format("2006-01-02 15:04 MST")
}

// tzSuffix renders " (Europe/Paris)" when set, empty otherwise.
func tzSuffix(tz string) string {
	if tz == "" {
		return ""
	}
	return " (" + tz + ")"
}

func formatScheduleLogs(s schedule.Schedule) string {
	if len(s.Runs) == 0 {
		return fmt.Sprintf("%s has no recorded runs yet", s.Name)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s last %d run(s):\n", s.Name, len(s.Runs))
	for _, r := range s.Runs {
		mark := "ok"
		if !r.Success {
			mark = "fail"
		}
		fmt.Fprintf(&sb, "  %s  %s  %s",
			r.StartedAt.Format("2006-01-02 15:04:05"),
			r.Duration.Truncate(time.Millisecond),
			mark,
		)
		if r.Error != "" {
			fmt.Fprintf(&sb, "  %s", truncate(r.Error, 120))
		} else if r.Preview != "" {
			fmt.Fprintf(&sb, "  %s", truncate(r.Preview, 120))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// lastRun returns the most recent run, or nil if none.
func lastRun(s schedule.Schedule) *schedule.RunLog {
	if len(s.Runs) == 0 {
		return nil
	}
	r := s.Runs[0]
	return &r
}

func formatLastRun(r schedule.RunLog) string {
	mark := "ok"
	if !r.Success {
		mark = "fail"
	}
	tail := ""
	if r.Error != "" {
		tail = " " + truncate(r.Error, 80)
	} else if r.Preview != "" {
		tail = " " + truncate(r.Preview, 80)
	}
	return fmt.Sprintf("%s %s%s",
		r.StartedAt.Format("2006-01-02 15:04"),
		mark,
		tail,
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
