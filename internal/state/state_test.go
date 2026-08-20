package state

import (
	"encoding/json"
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

func TestSaveOverwritesAReadOnlyDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions do not discriminate")
	}
	dir := t.TempDir()
	task := Task{SchemaVersion: 1, Name: "feat-42", Workspace: "/ws"}
	if err := Save(dir, task); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "feat-42.json")
	if err := os.Chmod(dest, 0o400); err != nil {
		t.Fatal(err)
	}
	task.Workspace = "/ws2"
	if err := Save(dir, task); err != nil {
		t.Fatalf("Save must replace a read-only destination by rename: %v", err)
	}
	got, err := Load(dir, "feat-42")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "/ws2" {
		t.Fatalf("second Save did not take effect: %+v", got)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()

	// Save two tasks
	task1 := Task{SchemaVersion: 1, Name: "task1", Workspace: "/ws"}
	task2 := Task{SchemaVersion: 1, Name: "task2", Workspace: "/ws"}
	if err := Save(dir, task1); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, task2); err != nil {
		t.Fatal(err)
	}

	// Create a .tmp file (leftover from failed write)
	if f, err := os.CreateTemp(dir, "task3.*.tmp"); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}

	// Create a directory named like a task
	dirLikeTask := filepath.Join(dir, "task4.json")
	if err := os.Mkdir(dirLikeTask, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %v", len(got), got)
	}

	found := make(map[string]bool)
	for _, name := range got {
		found[name] = true
	}

	if !found["task1"] || !found["task2"] {
		t.Fatalf("expected task1 and task2; got %v", got)
	}
	if found["task3"] || found["task4"] {
		t.Fatalf("should not list .tmp files or directories: %v", got)
	}
}

func TestLoadRejectsNewerSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "future.json")

	// Write a task with a future schema version directly
	futureTask := Task{
		SchemaVersion: 999,
		Name:          "future",
		Workspace:     "/ws",
	}
	b, err := json.MarshalIndent(futureTask, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Load(dir, "future")
	if err == nil {
		t.Fatal("expected an error for future schema version")
	}

	if e, ok := err.(*wkterr.E); !ok || e.Code != "WKT_STATE_VERSION" {
		t.Fatalf("expected WKT_STATE_VERSION error; got %v", err)
	}

	if !strings.Contains(err.Error(), "WKT_STATE_VERSION") {
		t.Fatalf("error must mention WKT_STATE_VERSION: %v", err)
	}
}
