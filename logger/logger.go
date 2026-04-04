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

func User(text string)    { log.Printf("%suser%s     %s", cyan, reset, truncate(text, 100)) }
func Nevinho(text string) { log.Printf("%snevinho%s  %s", green, reset, truncate(text, 100)) }
func Tool(name, detail string) {
	if detail != "" {
		log.Printf("         %s· %s %s%s", dim, name, truncate(detail, 80), reset)
	} else {
		log.Printf("         %s· %s%s", dim, name, reset)
	}
}
func Err(err error)       { log.Printf("%sfail%s     %v", red, reset, err) }
func Info(msg string)     { log.Printf("%s%s%s", dim, msg, reset) }

func Done(start time.Time, tokensIn, tokensOut, cacheRead int, tools []string) {
	secs := time.Since(start).Seconds()
	msg := fmt.Sprintf("%.1fs · %d in / %d out", secs, tokensIn, tokensOut)
	if cacheRead > 0 {
		msg += fmt.Sprintf(" · %d cached", cacheRead)
	}
	if len(tools) > 0 {
		msg += " · " + strings.Join(tools, ", ")
	}
	log.Printf("%sdone%s     %s", dim, reset, msg)
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
