package logger

import (
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	reset = "\033[0m"
	cyan  = "\033[36m"
	green = "\033[32m"
	red   = "\033[31m"
	dim   = "\033[2m"
)

func Init() {
	log.SetFlags(log.Ltime)
}

func User(text string)    { log.Printf("%suser%s     %s", cyan, reset, oneline(text)) }
func Voice(text string)   { log.Printf("%suser%s     (voice) %s", cyan, reset, oneline(text)) }
func Nevinho(text string) { log.Printf("%snevinho%s  %s", green, reset, oneline(text)) }
func Tool(name, detail string) {
	if detail != "" {
		log.Printf("         %s· %s %s%s", dim, name, truncate(detail, 80), reset)
	} else {
		log.Printf("         %s· %s%s", dim, name, reset)
	}
}

func ToolResult(name, result string, isError bool) {
	first := firstLine(result)
	if isError {
		log.Printf("         %s✗ %s: %s%s", red, name, truncate(first, 100), reset)
	} else {
		log.Printf("         %s✓ %s: %s%s", dim, name, truncate(first, 80), reset)
	}
}
func Err(err error)       { log.Printf("%sfail%s     %v", red, reset, err) }
func Info(msg string)     { log.Printf("%s%s%s", dim, msg, reset) }

func Done(start time.Time, tokensIn, tokensOut, cacheRead int, tools []string, cost float64) {
	secs := time.Since(start).Seconds()
	msg := fmt.Sprintf("%.1fs · %d in / %d out", secs, tokensIn, tokensOut)
	if cacheRead > 0 {
		msg += fmt.Sprintf(" · %d cached", cacheRead)
	}
	if cost > 0 {
		msg += fmt.Sprintf(" · $%.4f", cost)
	}
	if len(tools) > 0 {
		msg += " · " + strings.Join(tools, ", ")
	}
	log.Printf("%sdone%s     %s", dim, reset, msg)
}

func oneline(s string) string {
	return strings.ReplaceAll(s, "\n", " ")
}

func truncate(s string, max int) string {
	s = oneline(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		return s[:idx]
	}
	return s
}
