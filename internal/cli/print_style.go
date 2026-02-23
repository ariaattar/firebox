package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "1"
	ansiGreen  = "32"
	ansiYellow = "33"
	ansiCyan   = "36"
	ansiGray   = "90"
)

var (
	colorOnce    sync.Once
	colorEnabled bool
)

func styleHeader(s string) string  { return style(ansiBold+";"+ansiCyan, s) }
func styleInfo(s string) string    { return style(ansiCyan, s) }
func styleMuted(s string) string   { return style(ansiGray, s) }
func styleSuccess(s string) string { return style(ansiGreen, s) }
func styleWarn(s string) string    { return style(ansiYellow, s) }

func style(code, s string) string {
	if !enableColor() {
		return s
	}
	return fmt.Sprintf("\x1b[%sm%s%s", code, s, ansiReset)
}

func enableColor() bool {
	colorOnce.Do(func() {
		if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
			colorEnabled = false
			return
		}
		if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
			colorEnabled = false
			return
		}
		fi, err := os.Stdout.Stat()
		if err != nil {
			colorEnabled = false
			return
		}
		colorEnabled = (fi.Mode() & os.ModeCharDevice) != 0
	})
	return colorEnabled
}
