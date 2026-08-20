package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wkt/internal/wkterr"
)

func TestSaveIsAtomicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	task := Task{
		SchemaVersion: 1,
		Name:          "feat-42",
		Workspace:     "/ws",
		BaseEpoch:     time.Now().UTC().Truncate(time.Second),
		Repos: []Repo{{
			RelPath: "services/svc-a", StoreID: "services-svc-a-deadbeef",
			Branch: "feat-42", BaseSHA: "abc123", StoreWorktreeName: "svc-a",
		}},
	}
	if err := Save(dir, task); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("a temporary file survived the save: %s", e.Name())
		}
	}
	got, err := Load(dir, "feat-42")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repos) != 1 || got.Repos[0].StoreWorktreeName != "svc-a" {
		t.Fatalf("round-trip lost the store worktree name: %+v", got)
	}
	if !got.BaseEpoch.Equal(task.BaseEpoch) {
		t.Fatalf("base epoch %v != %v", got.BaseEpoch, task.BaseEpoch)
	}
}

func TestLoadMissingTaskIsTyped(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, "nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	if e, ok := err.(*wkterr.E); !ok || e.Code != "WKT_NO_TASK" {
		t.Fatalf("error must be typed as WKT_NO_TASK; got %v (ok=%v)", err, ok)
	}
	if !strings.Contains(err.Error(), "WKT_NO_TASK") {
		t.Fatalf("error %q must carry WKT_NO_TASK", err.Error())
	}
}
