package release

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// Website deploy target. The path is a constant, not configuration, so a bad
// .env value cannot point the deploy at another app on the same server.
const (
	WebsiteRoot  = "/home/ada.tools/wp-stage-sync"
	WebsiteOwner = "nobody:nogroup"
	WebsiteURL   = "https://ada.tools/wp-stage-sync/"
)

var (
	userRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

// WebsiteTarget reads DEPLOY_USER and DEPLOY_HOST from .env text and returns
// user@host. Both values reach an ssh command line, so anything but a plain
// name is refused.
func WebsiteTarget(env string) (string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(env))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	user, host := values["DEPLOY_USER"], values["DEPLOY_HOST"]
	if !userRe.MatchString(user) {
		return "", fmt.Errorf("DEPLOY_USER in .env is missing or not a plain user name")
	}
	if !hostRe.MatchString(host) {
		return "", fmt.Errorf("DEPLOY_HOST in .env is missing or not a plain host name")
	}
	return user + "@" + host, nil
}

// ChangedFiles returns the files whose content differs, from the output of
// `rsync --itemize-changes --checksum`. A line such as ".f..t.... install.sh"
// is a timestamp change only, and directories are not files to report.
func ChangedFiles(output string) []string {
	var files []string
	for _, line := range strings.Split(output, "\n") {
		code, name, ok := strings.Cut(line, " ")
		if !ok || len(code) < 2 || code[1] != 'f' {
			continue
		}
		if code[0] == '<' || code[0] == '>' || code[0] == 'c' {
			files = append(files, strings.TrimSpace(name))
		}
	}
	return files
}
