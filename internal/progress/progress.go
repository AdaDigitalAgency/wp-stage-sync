package progress

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Logger reports sync progress. Implementations differ for TUI vs CLI.
type Logger interface {
	Step(name string)
	Detail(msg string)
	Progress(current, total int)
	StepDone(msg string)
}

// --- CLI Logger (unattended mode) ---

type CLILogger struct {
	tty         bool
	active      bool   // an in-place line (no trailing newline) is shown
	barShown    bool   // the active line is a progress bar
	barComplete bool   // that bar reached its total
	label       string // last detail, shown next to the bar
}

func NewCLILogger() *CLILogger { return &CLILogger{tty: isTerminal(os.Stderr)} }

// commit ends the current in-place line with a newline, keeping it on screen.
func (l *CLILogger) commit() {
	if l.active {
		fmt.Fprintln(os.Stderr)
		l.active, l.barShown, l.barComplete = false, false, false
	}
}

func (l *CLILogger) Step(name string) {
	l.commit()
	l.label = ""
	fmt.Fprintf(os.Stderr, "%s ► %s\n", ts(), name)
}

func (l *CLILogger) Detail(msg string) {
	l.label = msg
	if !l.tty {
		fmt.Fprintf(os.Stderr, "%s   %s\n", ts(), msg)
		return
	}
	// Keep standalone details and completed bars on screen; overwrite an
	// in-progress bar so per-item detail+bar churn collapses onto one line.
	if l.active && (!l.barShown || l.barComplete) {
		l.commit()
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s   %s", ts(), msg)
	l.active, l.barShown = true, false
}

func (l *CLILogger) Progress(current, total int) {
	if total <= 0 {
		return
	}
	bar := renderBar(current, total, 24)
	if !l.tty {
		// Non-terminal (cron/log): print only the final state to avoid spam.
		if current >= total {
			fmt.Fprintf(os.Stderr, "%s   %s %d/%d\n", ts(), bar, current, total)
		}
		return
	}
	line := fmt.Sprintf("%s   %s %d/%d", ts(), bar, current, total)
	if l.label != "" {
		line += "  " + l.label
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s", line)
	l.active, l.barShown, l.barComplete = true, true, current >= total
}

func (l *CLILogger) StepDone(msg string) {
	l.commit()
	l.label = ""
	fmt.Fprintf(os.Stderr, "%s ✓ %s\n", ts(), msg)
}

// --- Nop Logger ---

type NopLogger struct{}

func (NopLogger) Step(string)       {}
func (NopLogger) Detail(string)     {}
func (NopLogger) Progress(int, int) {}
func (NopLogger) StepDone(string)   {}

// --- Helpers ---

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func ts() string {
	return fmt.Sprintf("[%s]", time.Now().Format("15:04:05"))
}

func renderBar(current, total, width int) string {
	if total <= 0 {
		return ""
	}
	filled := width * current / total
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
