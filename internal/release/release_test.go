package release

import (
	"slices"
	"strings"
	"testing"
)

const changelog = `# Changelog

Intro text.

## [Unreleased]

### Fixed

- A fix.

## [0.6.0] - 2026-06-11

### Added

- Old thing.
`

func TestBump(t *testing.T) {
	for _, c := range [][3]string{{"0.6.0\n", "minor", "0.7.0"}, {"0.6.1", "patch", "0.6.2"}, {"0.6.1", "major", "1.0.0"}} {
		if got, err := Bump(c[0], c[1]); err != nil || got != c[2] {
			t.Errorf("Bump(%q, %q) = %q, %v", c[0], c[1], got, err)
		}
	}
	if _, err := Bump("0.6.0", "huge"); err == nil {
		t.Error("unknown size: want error")
	}
	if _, err := Bump("abc", "patch"); err == nil {
		t.Error("bad version: want error")
	}
}

func TestCutChangelog(t *testing.T) {
	got, err := CutChangelog(changelog, "0.7.0", "2026-11-01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## [Unreleased]\n\n## [0.7.0] - 2026-11-01\n\n### Fixed\n\n- A fix.\n\n## [0.6.0]") {
		t.Errorf("cut:\n%s", got)
	}
	notes, err := VersionNotes(got, "0.7.0")
	if err != nil || notes != "### Fixed\n\n- A fix." {
		t.Errorf("notes = %q, %v", notes, err)
	}
	if _, err := CutChangelog(got, "0.8.0", "2026-11-02"); err == nil {
		t.Error("empty Unreleased: want error")
	}
	if _, err := CutChangelog(changelog, "0.6.0", "2026-11-02"); err == nil {
		t.Error("existing version: want error")
	}
	if _, err := UnreleasedNotes("# Changelog\n"); err == nil {
		t.Error("missing section: want error")
	}
	if _, err := VersionNotes(got, "9.9.9"); err == nil {
		t.Error("unknown version: want error")
	}
}

func TestCutWhatsNew(t *testing.T) {
	src := "var Entries = map[string][]string{\n\t\"unreleased\": {\n\t\t\"unreleased line\",\n\t},\n}\n"
	got, ok, err := CutWhatsNew(src, "0.7.0")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !strings.Contains(got, "\t\"0.7.0\": {\n\t\t\"unreleased line\"") {
		t.Errorf("cut:\n%s", got)
	}
	if _, ok, _ := CutWhatsNew(got, "0.8.0"); ok {
		t.Error("no unreleased key: want false")
	}
	both := strings.Replace(src, "}\n", "\t\"0.7.0\": {},\n}\n", 1)
	if _, _, err := CutWhatsNew(both, "0.7.0"); err == nil {
		t.Error("existing key: want error")
	}
}

func TestRunStateOf(t *testing.T) {
	cases := []struct {
		status, conclusion string
		want               RunState
	}{
		{"queued", "", RunPending},
		{"in_progress", "", RunPending},
		{"completed", "success", RunPassed},
		{"completed", "failure", RunFailed},
		{"completed", "cancelled", RunFailed},
	}
	for _, c := range cases {
		if got := RunStateOf(c.status, c.conclusion); got != c.want {
			t.Errorf("RunStateOf(%q, %q) = %s", c.status, c.conclusion, got)
		}
	}
}

func readyState() State {
	return State{
		Version: "0.7.0", Branch: "main", Clean: true, HeadSubject: "Release v0.7.0",
		Head: "aaa", OriginBehind: true, CI: RunMissing, ReleaseRun: RunMissing,
	}
}

func TestPlanShipResumes(t *testing.T) {
	all := []Step{StepPushMain, StepWaitCI, StepTag, StepPushTag, StepWaitRelease}
	cases := []struct {
		name   string
		change func(*State)
		want   []Step
	}{
		{"nothing done", func(*State) {}, all},
		{"pushed", func(s *State) { s.OnOrigin = true; s.CI = RunPending }, all[1:]},
		{"ci green", func(s *State) { s.OnOrigin = true; s.CI = RunPassed }, all[2:]},
		{"tagged locally", func(s *State) { s.OnOrigin = true; s.CI = RunPassed; s.LocalTag = "aaa" }, all[3:]},
		{"tag pushed", func(s *State) {
			s.OnOrigin = true
			s.CI = RunPassed
			s.LocalTag, s.RemoteTag = "aaa", "aaa"
			s.ReleaseRun = RunPending
		}, all[4:]},
		{"finished", func(s *State) {
			s.OnOrigin = true
			s.CI = RunPassed
			s.LocalTag, s.RemoteTag = "aaa", "aaa"
			s.ReleaseRun = RunPassed
			s.ReleaseExists = true
		}, nil},
	}
	for _, c := range cases {
		s := readyState()
		c.change(&s)
		got, err := PlanShip(s)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: PlanShip = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

func TestPlanShipRefuses(t *testing.T) {
	cases := map[string]func(*State){
		"branch":     func(s *State) { s.Branch = "feature" },
		"dirty":      func(s *State) { s.Clean = false },
		"subject":    func(s *State) { s.HeadSubject = "fix: thing" },
		"diverged":   func(s *State) { s.OriginBehind = false },
		"local tag":  func(s *State) { s.LocalTag = "zzz" },
		"remote tag": func(s *State) { s.RemoteTag = "zzz" },
		"ci":         func(s *State) { s.CI = RunFailed },
		"release":    func(s *State) { s.ReleaseRun = RunFailed },
	}
	for name, change := range cases {
		s := readyState()
		change(&s)
		if _, err := PlanShip(s); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
