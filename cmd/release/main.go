// Command release cuts and ships a wp-stage-sync release.
//
//	go run ./cmd/release <patch|minor|major> [--no-ship] [--yes]
//	go run ./cmd/release ship [--yes] [--dry-run] [--website]
//	go run ./cmd/release website [--go]
//	go run ./cmd/release notes <version>
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/release"
)

const (
	versionFile   = ".version"
	changelogFile = "CHANGELOG.md"
	entriesFile   = "internal/whatsnew/entries.go"
	envFile       = ".env"
	websiteDir    = "website/"
	userAgent     = "ada-claude-code/1.0 (+ada.agency)"

	pollInterval = 15 * time.Second
	waitTimeout  = 30 * time.Minute
	// A run should exist soon after a push. Past this, the workflow did not start.
	runStartTimeout = 3 * time.Minute
)

var releaseFiles = []string{versionFile, changelogFile, entriesFile}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release <patch|minor|major|ship|notes> [flags]")
	}
	cmd, rest := args[0], args[1:]
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	noShip := flags.Bool("no-ship", false, "stop after the release commit")
	yes := flags.Bool("yes", false, "ship without asking")
	dryRun := flags.Bool("dry-run", false, "print the ship steps and stop")
	website := flags.Bool("website", false, "deploy the website after shipping, without asking")
	publish := flags.Bool("go", false, "publish the website, not only a dry run")
	if cmd == "notes" {
		if len(rest) != 1 {
			return errors.New("usage: release notes <version>")
		}
		return printNotes(rest[0])
	}
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if cmd == "website" {
		return deployWebsite(*publish)
	}
	if cmd == "ship" {
		return shipAndOfferWebsite(*yes, *dryRun, *website)
	}
	if err := cut(cmd); err != nil {
		return err
	}
	if *noShip {
		fmt.Println("Release commit is ready. Run `make ship` to publish it.")
		return nil
	}
	return shipAndOfferWebsite(*yes, false, *website)
}

// shipAndOfferWebsite ships, then deploys the website on request. The website
// never blocks a release: a failure here is reported after the release is out.
func shipAndOfferWebsite(yes, dryRun, website bool) error {
	if err := ship(yes, dryRun); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	if !website {
		if yes || !ask("Deploy the website too? [y/N] ") {
			fmt.Println("Website not deployed. Run `make deploy-website` to check it.")
			return nil
		}
	}
	return deployWebsite(true)
}

// deployWebsite compares website/ with the server and publishes the changes.
// It never uses --delete: the server folder is not rebuilt from this repo.
func deployWebsite(publish bool) error {
	dirty, err := output("git", "status", "--porcelain", "--", websiteDir)
	if err != nil {
		return err
	}
	if dirty != "" {
		return fmt.Errorf("website/ has uncommitted changes:\n%s", dirty)
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w (see .env.example)", envFile, err)
	}
	target, err := release.WebsiteTarget(string(env))
	if err != nil {
		return err
	}
	dest := target + ":" + release.WebsiteRoot + "/"
	args := []string{"-rlptD", "--checksum", "--exclude=.DS_Store"}

	plan, err := output("rsync", append(append([]string{}, args...), "--dry-run", "--itemize-changes", websiteDir, dest)...)
	if err != nil {
		return err
	}
	changed := release.ChangedFiles(plan)
	if len(changed) == 0 {
		fmt.Println("Website files are up to date.")
	} else {
		fmt.Printf("Website files to publish: %s\n", strings.Join(changed, ", "))
	}
	if !publish {
		if len(changed) > 0 {
			fmt.Println("Dry run. Run `make deploy-website GO=1` to publish.")
			return nil
		}
		return verifyWebsite()
	}
	if len(changed) > 0 {
		if err := exec1("rsync", append(args, websiteDir, dest)...); err != nil {
			return err
		}
		// macOS openrsync ignores --chmod, so the server step sets the modes.
		root := release.WebsiteRoot
		remote := fmt.Sprintf("find %s -type d -exec chmod 755 {} + && find %s -type f -exec chmod 644 {} + && chown -R %s %s",
			root, root, release.WebsiteOwner, root)
		if err := exec1("ssh", "-o", "BatchMode=yes", target, remote); err != nil {
			return err
		}
	}
	return verifyWebsite()
}

// verifyWebsite compares the live page with the local file.
func verifyWebsite() error {
	local, err := os.ReadFile(websiteDir + "index.html")
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, release.WebsiteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	live, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", release.WebsiteURL, resp.StatusCode)
	}
	if sha256.Sum256(live) != sha256.Sum256(local) {
		return fmt.Errorf("%s does not match website/index.html", release.WebsiteURL)
	}
	fmt.Printf("✓ %s matches website/index.html\n", release.WebsiteURL)
	return nil
}

func printNotes(version string) error {
	text, err := os.ReadFile(changelogFile)
	if err != nil {
		return err
	}
	notes, err := release.VersionNotes(string(text), version)
	if err != nil {
		return err
	}
	fmt.Println(notes)
	return nil
}

// cut runs the checks, updates the release files and commits them.
func cut(size string) error {
	current, err := os.ReadFile(versionFile)
	if err != nil {
		return err
	}
	version, err := release.Bump(string(current), size)
	if err != nil {
		return err
	}
	if err := requireCleanMain(); err != nil {
		return err
	}
	changelog, err := os.ReadFile(changelogFile)
	if err != nil {
		return err
	}
	notes, err := release.UnreleasedNotes(string(changelog))
	if err != nil {
		return err
	}
	if err := checks(); err != nil {
		return err
	}

	fmt.Printf("Cutting %s\n", version)
	if err := applyCut(version, size, string(changelog)); err != nil {
		restore()
		return err
	}
	// The cut changes files that tests read, so they run again on the result.
	if err := checks(); err != nil {
		restore()
		return fmt.Errorf("checks failed after the cut, files restored: %w", err)
	}
	if err := exec1("git", append([]string{"add", "--"}, releaseFiles...)...); err != nil {
		restore()
		return err
	}
	return exec1("git", "commit", "-m", release.Subject(version)+"\n\n"+notes)
}

func applyCut(version, size, changelog string) error {
	date := time.Now().UTC().Format("2006-01-02")
	cutLog, err := release.CutChangelog(changelog, version, date)
	if err != nil {
		return err
	}
	entries, err := os.ReadFile(entriesFile)
	if err != nil {
		return err
	}
	cutEntries, hadLines, err := release.CutWhatsNew(string(entries), version)
	if err != nil {
		return err
	}
	if !hadLines && size != "patch" {
		fmt.Printf("Note: %s has no What's new lines.\n", version)
	}
	for file, text := range map[string]string{
		versionFile:   version + "\n",
		changelogFile: cutLog,
		entriesFile:   cutEntries,
	} {
		if err := os.WriteFile(file, []byte(text), 0644); err != nil {
			return err
		}
	}
	return nil
}

func restore() {
	_ = exec1("git", append([]string{"checkout", "--"}, releaseFiles...)...)
}

func requireCleanMain() error {
	branch, err := output("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if branch != "main" {
		return fmt.Errorf("on branch %q: release from main", branch)
	}
	status, err := output("git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("the working tree is not clean:\n%s", status)
	}
	return nil
}

func checks() error {
	unformatted, err := output("gofmt", "-l", "cmd", "internal")
	if err != nil {
		return err
	}
	if unformatted != "" {
		return fmt.Errorf("gofmt needed:\n%s", unformatted)
	}
	for _, args := range [][]string{{"vet", "./..."}, {"test", "./..."}, {"build", "./..."}} {
		if err := exec1("go", args...); err != nil {
			return err
		}
	}
	return nil
}

func ship(yes, dryRun bool) error {
	version, err := readVersion()
	if err != nil {
		return err
	}
	state, err := readState(version)
	if err != nil {
		return err
	}
	steps, err := release.PlanShip(state)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		fmt.Printf("v%s is already shipped.\n", version)
		return nil
	}
	fmt.Printf("Ship v%s: %v\n", version, steps)
	if dryRun {
		return nil
	}
	if !yes {
		if err := confirm(); err != nil {
			return err
		}
	}
	for _, step := range steps {
		fmt.Printf("► %s\n", step)
		if err := runStep(step, version, state.Head); err != nil {
			return fmt.Errorf("%s: %w (run `make ship` to resume)", step, err)
		}
	}
	fmt.Printf("✓ Shipped v%s\n", version)
	return nil
}

func confirm() error {
	if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("no terminal to ask on: pass --yes")
	}
	if !ask("Push to GitHub and publish? [y/N] ") {
		return errors.New("stopped")
	}
	return nil
}

func ask(question string) bool {
	fmt.Print(question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.ToLower(strings.TrimSpace(answer)) == "y"
}

func runStep(step release.Step, version, head string) error {
	tag := "v" + version
	switch step {
	case release.StepPushMain:
		return exec1("git", "push", "--no-follow-tags", "origin", "HEAD:main")
	case release.StepWaitCI:
		return waitForRun("ci.yml", head)
	case release.StepTag:
		return exec1("git", "tag", "-a", tag, "-m", release.Subject(version))
	case release.StepPushTag:
		return exec1("git", "push", "origin", "refs/tags/"+tag)
	case release.StepWaitRelease:
		if err := waitForRun("release.yml", head); err != nil {
			return err
		}
		if !releaseExists(tag) {
			return fmt.Errorf("the workflow passed but release %s has no checksums.txt", tag)
		}
		return nil
	}
	return fmt.Errorf("unknown step %s", step)
}

func readVersion() (string, error) {
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readState(version string) (release.State, error) {
	tag := "v" + version
	if err := exec1("git", "fetch", "--quiet", "origin", "main"); err != nil {
		return release.State{}, err
	}
	s := release.State{Version: version}
	var err error
	if s.Branch, err = output("git", "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return s, err
	}
	status, err := output("git", "status", "--porcelain")
	if err != nil {
		return s, err
	}
	s.Clean = status == ""
	if s.HeadSubject, err = output("git", "log", "-1", "--format=%s"); err != nil {
		return s, err
	}
	if s.Head, err = output("git", "rev-parse", "HEAD"); err != nil {
		return s, err
	}
	s.OnOrigin = isAncestor("HEAD", "origin/main")
	s.OriginBehind = isAncestor("origin/main", "HEAD")
	s.LocalTag, _ = output("git", "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}")
	remote, err := output("git", "ls-remote", "origin", "refs/tags/"+tag+"^{}")
	if err != nil {
		return s, err
	}
	if fields := strings.Fields(remote); len(fields) > 0 {
		s.RemoteTag = fields[0]
	}
	if s.CI, err = runState("ci.yml", s.Head); err != nil {
		return s, err
	}
	if s.ReleaseRun, err = runState("release.yml", s.Head); err != nil {
		return s, err
	}
	s.ReleaseExists = releaseExists(tag)
	return s, nil
}

func isAncestor(a, b string) bool {
	return exec.Command("git", "merge-base", "--is-ancestor", a, b).Run() == nil
}

// runState reads the newest run of a workflow for a commit. An error means the
// state is unknown, for example after a GitHub API failure. It does not mean
// the run failed.
func runState(workflow, commit string) (release.RunState, error) {
	out, err := output("gh", "run", "list", "--workflow", workflow, "--commit", commit,
		"--limit", "1", "--json", "status,conclusion")
	if err != nil {
		// The first release that adds a workflow pushes it, so GitHub does not
		// know that workflow yet. That means no run, not a failure.
		if strings.Contains(err.Error(), "not found on the default branch") {
			return release.RunMissing, nil
		}
		return "", err
	}
	var runs []struct{ Status, Conclusion string }
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return "", fmt.Errorf("reading gh output: %w", err)
	}
	if len(runs) == 0 {
		return release.RunMissing, nil
	}
	return release.RunStateOf(runs[0].Status, runs[0].Conclusion), nil
}

// waitForRun polls instead of using `gh run watch`, which exits non-zero on a
// GitHub API error even when the run itself is fine.
func waitForRun(workflow, commit string) error {
	started := time.Now()
	for {
		state, err := runState(workflow, commit)
		switch {
		case err != nil:
			fmt.Printf("  could not read %s, retrying: %v\n", workflow, err)
		case state == release.RunPassed:
			return nil
		case state == release.RunFailed:
			return fmt.Errorf("%s failed for %s", workflow, commit)
		case state == release.RunMissing && time.Since(started) > runStartTimeout:
			return fmt.Errorf("%s did not start for %s", workflow, commit)
		}
		if time.Since(started) > waitTimeout {
			return fmt.Errorf("timed out waiting for %s", workflow)
		}
		time.Sleep(pollInterval)
	}
}

func releaseExists(tag string) bool {
	out, err := output("gh", "release", "view", tag, "--json", "assets", "--jq", ".assets[].name")
	return err == nil && slices.Contains(strings.Fields(out), "checksums.txt")
}

func exec1(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}
