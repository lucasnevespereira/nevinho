package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts both standard 5 field cron expressions ("0 9 * * *"),
// the @yearly/@monthly/@weekly/@daily/@hourly aliases, and the @every
// duration form. Seconds are not supported on purpose. Sub minute
// scheduling is rejected at create time anyway.
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func parseCron(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	return parser.Parse(expr)
}

// minInterval probes the schedule by asking for two consecutive next
// fires. The smaller delta is the effective minimum interval. We use it
// to reject schedules that fire too frequently regardless of how the
// expression is shaped.
func minInterval(s cron.Schedule) time.Duration {
	now := time.Now()
	a := s.Next(now)
	b := s.Next(a)
	return b.Sub(a)
}

func newID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

func trim(s string) string {
	return strings.TrimSpace(s)
}

// formatDuration renders a time.Duration in a way humans can read in chat.
// 90s → "1m30s", 3600s → "1h", 86400s → "24h0m0s" → trimmed.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return d.Truncate(time.Second).String()
	}
	return d.Truncate(time.Minute).String()
}
