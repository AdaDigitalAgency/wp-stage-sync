package release

import "fmt"

// Step is one action of shipping a release commit.
type Step string

const (
	StepPushMain    Step = "push-main"
	StepWaitCI      Step = "wait-ci"
	StepTag         Step = "tag"
	StepPushTag     Step = "push-tag"
	StepWaitRelease Step = "wait-release"
)

// RunState is the result of a GitHub Actions run for a commit or tag.
type RunState string

const (
	RunMissing RunState = "missing" // no run exists yet
	RunPending RunState = "pending" // queued or in progress
	RunPassed  RunState = "passed"
	RunFailed  RunState = "failed"
)

// RunStateOf maps the status and conclusion that GitHub reports. Only a
// completed run with a bad conclusion counts as a failure. An API error is not
// a run state: the caller retries it.
func RunStateOf(status, conclusion string) RunState {
	if status != "completed" {
		return RunPending
	}
	if conclusion == "success" {
		return RunPassed
	}
	return RunFailed
}

// State is what the release commit looks like right now.
type State struct {
	Version       string // from .version
	Branch        string
	Clean         bool
	HeadSubject   string
	Head          string   // commit hash of HEAD
	OriginBehind  bool     // origin/main is an ancestor of HEAD
	OnOrigin      bool     // HEAD is on origin/main
	LocalTag      string   // commit of the local tag, empty if none
	RemoteTag     string   // commit of the remote tag, empty if none
	CI            RunState // CI run for HEAD
	ReleaseRun    RunState // release run for the tag
	ReleaseExists bool     // the GitHub release has its assets
}

// Subject is the commit subject of the release commit for version.
func Subject(version string) string {
	return "Release v" + version
}

// PlanShip returns the steps still to do, or an error when shipping is not
// safe. It reads only s, so a second run resumes where the first one stopped.
func PlanShip(s State) ([]Step, error) {
	tag := "v" + s.Version
	if s.Branch != "main" {
		return nil, fmt.Errorf("on branch %q: ship from main", s.Branch)
	}
	if !s.Clean {
		return nil, fmt.Errorf("the working tree is not clean")
	}
	if s.HeadSubject != Subject(s.Version) {
		return nil, fmt.Errorf("HEAD is %q, want %q: run the release first", s.HeadSubject, Subject(s.Version))
	}
	if !s.OnOrigin && !s.OriginBehind {
		return nil, fmt.Errorf("origin/main has commits that HEAD does not have")
	}
	for _, t := range []string{s.LocalTag, s.RemoteTag} {
		if t != "" && t != s.Head {
			return nil, fmt.Errorf("tag %s is on another commit (%s)", tag, t)
		}
	}
	if s.CI == RunFailed {
		return nil, fmt.Errorf("CI failed for %s", s.Head)
	}
	if s.ReleaseRun == RunFailed {
		return nil, fmt.Errorf("the release workflow failed for %s", tag)
	}

	var steps []Step
	if !s.OnOrigin {
		steps = append(steps, StepPushMain)
	}
	if s.CI != RunPassed && s.RemoteTag == "" {
		steps = append(steps, StepWaitCI)
	}
	if s.LocalTag == "" {
		steps = append(steps, StepTag)
	}
	if s.RemoteTag == "" {
		steps = append(steps, StepPushTag)
	}
	if !s.ReleaseExists {
		steps = append(steps, StepWaitRelease)
	}
	return steps, nil
}
