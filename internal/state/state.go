package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wkt/internal/wkterr"
)

const SchemaVersion = 1

type Repo struct {
	RelPath           string `json:"rel_path"`
	AbsPath           string `json:"abs_path"`
	StoreID           string `json:"store_id"`
	Branch            string `json:"branch"`
	BaseSHA           string `json:"base_sha"`
	BaseRef           string `json:"base_ref"`
	WorktreePath      string `json:"worktree_path"`
	StoreWorktreeName string `json:"store_worktree_name"`
	BasePinRef        string `json:"base_pin_ref"`
}

type LinkSlot struct {
	RelPath string `json:"rel_path"`
	Target  string `json:"target"`
	Type    string `json:"type"`
	Hash    string `json:"hash,omitempty"`
}

type Task struct {
	SchemaVersion      int               `json:"schema_version"`
	Name               string            `json:"name"`
	Container          string            `json:"container"`
	Workspace          string            `json:"workspace"`
	WorkspaceSpellings []string          `json:"workspace_spellings"`
	BaseEpoch          time.Time         `json:"base_epoch"`
	Repos              []Repo            `json:"repos"`
	Links              []LinkSlot        `json:"links"`
	PerimeterCoverage  []string          `json:"perimeter_coverage,omitempty"`
	PerimeterHashes    map[string]string `json:"perimeter_hashes,omitempty"`
}

func path(dir, name string) string { return filepath.Join(dir, name+".json") }

// Save writes a task to a temporary file in the same directory, then renames it
// for atomic writes. This ensures the final file is either complete or not present.
func Save(dir string, t Task) error {
	if t.SchemaVersion == 0 {
		t.SchemaVersion = SchemaVersion
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot create the state directory").WithPath(dir)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot encode task state").WithFound(err.Error())
	}
	tmp, err := os.CreateTemp(dir, t.Name+".*.tmp")
	if err != nil {
		return wkterr.New("WKT_STATE_WRITE", "cannot create a temporary state file").WithPath(dir)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot write task state").WithPath(tmp.Name())
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot close the temporary state file").WithPath(tmp.Name())
	}
	if err := os.Rename(tmp.Name(), path(dir, t.Name)); err != nil {
		_ = os.Remove(tmp.Name())
		return wkterr.New("WKT_STATE_WRITE", "cannot commit task state").WithPath(path(dir, t.Name))
	}
	return nil
}

// Load reads a task from disk by name.
func Load(dir, name string) (Task, error) {
	b, err := os.ReadFile(path(dir, name))
	if err != nil {
		return Task{}, wkterr.New("WKT_NO_TASK", "no such task").WithPath(path(dir, name))
	}
	var t Task
	if err := json.Unmarshal(b, &t); err != nil {
		return Task{}, wkterr.New("WKT_STATE_CORRUPT", "task state is not readable").
			WithPath(path(dir, name)).WithFound(err.Error())
	}
	return t, nil
}

// List returns the names of all saved tasks in a directory.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // no tasks yet is not an error
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return out, nil
}
