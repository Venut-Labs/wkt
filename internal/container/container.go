package container

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"wkt/internal/paths"
	"wkt/internal/wkterr"
)

type C struct {
	Root      string
	Workspace string
}

func (c C) StoreDir() string   { return filepath.Join(c.Root, "store") }
func (c C) TreesDir() string   { return filepath.Join(c.Root, "trees") }
func (c C) StateDir() string   { return filepath.Join(c.Root, "state", "tasks") }
func (c C) StagingDir() string { return filepath.Join(c.Root, "staging") }

// ConfigDir holds the container's own state, as opposed to StateDir, which
// holds one file per task.
func (c C) ConfigDir() string { return filepath.Join(c.Root, "state") }

func (c C) TreePath(task string) string { return filepath.Join(c.TreesDir(), task) }

func Locate(workspace string) (C, error) {
	ws, err := paths.Canonical(workspace)
	if err != nil {
		return C{}, err
	}
	sibling := ws + ".worktrees"
	if writable(filepath.Dir(ws)) {
		return C{Root: sibling, Workspace: ws}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return C{}, wkterr.New("WKT_NO_CONTAINER", "cannot place the container").WithFound(err.Error())
	}
	sum := sha256.Sum256([]byte(ws))
	id := hex.EncodeToString(sum[:])[:12]
	root := filepath.Join(home, ".local", "state", "wkt", id)
	if paths.IsUnder(root, ws) {
		return C{}, wkterr.New("WKT_NO_CONTAINER", "the fallback container would live inside the workspace").
			WithPath(root).
			WithFound("workspace: " + ws).
			WithRemedy("configure the container location explicitly")
	}
	return C{Root: root, Workspace: ws}, nil
}

func writable(dir string) bool {
	probe := filepath.Join(dir, ".wkt-write-probe-"+strconv.Itoa(os.Getpid()))
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

func Create(c C) error {
	for _, d := range []string{c.StoreDir(), c.TreesDir(), c.StateDir(), c.StagingDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return wkterr.New("WKT_NO_CONTAINER", "cannot create the container").
				WithPath(d).WithFound(err.Error())
		}
	}
	return nil
}

func Lock(c C) (func(), error) {
	path := filepath.Join(c.Root, ".wkt.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, wkterr.New("WKT_CONTAINER_UNUSABLE", "cannot open the container lock").
			WithPath(path).WithFound(err.Error())
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := os.ReadFile(path)
		_ = f.Close()
		return nil, wkterr.New("WKT_LOCKED", "another wkt process holds the container lock").
			WithPath(path).WithFound(string(holder)).
			WithRemedy("wait for it to finish", "or remove the lock file if no wkt process is running")
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
