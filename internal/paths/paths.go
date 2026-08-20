package paths

import (
	"path/filepath"
	"runtime"
	"strings"
)

func Canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	// Path doesn't exist; try to resolve parent directories recursively
	parent := filepath.Dir(abs)
	if parent == abs {
		// We're at the root
		return abs, nil
	}
	resolvedParent, _ := Canonical(parent)
	// Return the parent with the original filename appended
	return filepath.Join(resolvedParent, filepath.Base(abs)), nil
}

func Spellings(p string) []string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	canon, _ := Canonical(p)
	out := []string{abs}
	add := func(s string) {
		for _, existing := range out {
			if existing == s {
				return
			}
		}
		out = append(out, s)
	}
	add(canon)
	if runtime.GOOS == "darwin" {
		for _, s := range []string{abs, canon} {
			if strings.HasPrefix(s, "/private/") {
				add(strings.TrimPrefix(s, "/private"))
			} else {
				add("/private" + s)
			}
		}
	}
	return out
}

func IsUnder(child, parent string) bool {
	c, _ := Canonical(child)
	p, _ := Canonical(parent)
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
