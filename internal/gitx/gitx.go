package gitx

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"

	"wkt/internal/wkterr"
)

func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errb.String())
		if stderr == "" {
			// Fallback to exec error when stderr is empty
			stderr = err.Error()
		}
		first := strings.SplitN(stderr, "\n", 2)[0]
		return "", wkterr.New("WKT_GIT_FAILED", "git "+args[0]+" failed").
			WithPath(dir).WithFound(first)
	}
	return strings.TrimSpace(out.String()), nil
}

func RunOK(dir string, args ...string) bool {
	_, err := Run(dir, args...)
	return err == nil
}

func parseVersion(out string) (int, int, error) {
	fields := strings.Fields(out) // "git version 2.50.1"
	if len(fields) < 3 {
		return 0, 0, wkterr.New("WKT_GIT_VERSION", "cannot parse git version").WithFound(out)
	}
	parts := strings.Split(fields[2], ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, wkterr.New("WKT_GIT_VERSION", "cannot parse git version").WithFound(out)
	}
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor, nil
}

func Version() (int, int, error) {
	out, err := Run(".", "--version")
	if err != nil {
		return 0, 0, err
	}
	return parseVersion(out)
}
