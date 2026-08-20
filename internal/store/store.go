package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"wkt/internal/gitx"
	"wkt/internal/paths"
	"wkt/internal/wkterr"
)

// ID is a collision-free function of the workspace-relative path — never the
// basename, so services/api and tools/api cannot collide (spec §5.2).
func ID(relPath, absPath string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, relPath)
	canon, _ := paths.Canonical(absPath)
	sum := sha256.Sum256([]byte(canon))
	return strings.Trim(slug, "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// Ensure performs the six steps of spec §5.2, in order. The base pin is written
// FIRST so no gc window exists before the store references the base (H11).
func Ensure(storeDir, repoAbs, relPath, taskName, baseSHA string) (string, error) {
	pin := "refs/wkt/base/" + taskName
	if _, err := gitx.Run(repoAbs, "update-ref", pin, baseSHA); err != nil {
		return "", wkterr.New("WKT_PIN_FAILED", "cannot pin the base commit in the workspace repository").
			WithRepo(relPath).WithPath(repoAbs)
	}

	sp := filepath.Join(storeDir, ID(relPath, repoAbs)+".git")
	if _, err := os.Stat(sp); err == nil {
		return sp, nil // idempotent
	}

	if _, err := gitx.Run(storeDir, "clone", "--shared", "--bare", "-q", repoAbs, sp); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot mirror the repository").
			WithRepo(relPath).WithPath(sp)
	}
	// De-borrow: copy the objects in, then drop the alternates pointer, so the
	// store survives deletion or re-clone of the workspace repository (spec §5.2).
	if _, err := gitx.Run(sp, "repack", "-a", "-d", "-q"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot repack the store").WithRepo(relPath).WithPath(sp)
	}
	if err := os.Remove(filepath.Join(sp, "objects", "info", "alternates")); err != nil && !os.IsNotExist(err) {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot de-borrow the store").WithRepo(relPath).WithPath(sp)
	}

	origin, err := gitx.Run(repoAbs, "remote", "get-url", "origin")
	if err == nil && origin != "" {
		if _, err := gitx.Run(sp, "remote", "set-url", "origin", origin); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot point the store at the real origin").WithRepo(relPath)
		}
	} else {
		_, _ = gitx.Run(sp, "remote", "remove", "origin")
	}
	// Bare clones set NO fetch refspec; without this refs/remotes/* never exist,
	// which silently breaks sync and the unpushed-commit guard (spec H15).
	if _, err := gitx.Run(sp, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the origin refspec").WithRepo(relPath)
	}
	// Second remote: the workspace repository, so a task can branch from work the
	// developer has committed locally and not pushed (spec §5.2).
	if _, err := gitx.Run(sp, "remote", "add", "workspace", repoAbs); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot add the workspace remote").WithRepo(relPath)
	}
	if _, err := gitx.Run(sp, "config", "remote.workspace.fetch", "+refs/heads/*:refs/remotes/ws/*"); err != nil {
		return "", wkterr.New("WKT_STORE_CREATE", "cannot configure the workspace refspec").WithRepo(relPath)
	}
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"core.hooksPath", "/dev/null"}} {
		if _, err := gitx.Run(sp, "config", kv[0], kv[1]); err != nil {
			return "", wkterr.New("WKT_STORE_CREATE", "cannot harden the store").WithRepo(relPath)
		}
	}
	return sp, nil
}

func FetchWorkspace(storePath string) error {
	if _, err := gitx.Run(storePath, "fetch", "-q", "workspace"); err != nil {
		return wkterr.New("WKT_FETCH_FAILED", "cannot fetch from the workspace repository").WithPath(storePath)
	}
	return nil
}

func HasObject(storePath, sha string) bool {
	return gitx.RunOK(storePath, "cat-file", "-e", sha)
}
