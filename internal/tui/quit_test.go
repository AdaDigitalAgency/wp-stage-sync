package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(m model, s string) (model, tea.Cmd) {
	var msg tea.KeyMsg
	if s == "ctrl+c" {
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	} else {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestQuitDuringRunNeedsConfirmation(t *testing.T) {
	for _, step := range []step{stepRunning, stepPromoteRunning, stepRestoreRunning} {
		m := model{step: step}
		m, cmd := key(m, "q")
		if quits(cmd) || !m.confirmQuit {
			t.Fatalf("step %v: q quit at once", step)
		}
		m, cmd = key(m, "n")
		if quits(cmd) || m.confirmQuit {
			t.Fatalf("step %v: n did not cancel", step)
		}
		m, _ = key(m, "ctrl+c")
		if _, cmd = key(m, "y"); !quits(cmd) {
			t.Fatalf("step %v: y did not quit", step)
		}
	}
	// Outside a run, q quits at once.
	if _, cmd := key(model{step: stepDone}, "q"); !quits(cmd) {
		t.Error("q on the done screen did not quit")
	}
}
