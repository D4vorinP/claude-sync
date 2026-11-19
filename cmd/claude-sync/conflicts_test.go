package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tawanorg/claude-sync/internal/sync"
)

const conflictedKey = "projects/p/s.jsonl"

func conflictFixture(t *testing.T) (claudeDir string, state *sync.SyncState, conflicts []conflictFile, baseHash string, seen time.Time) {
	t.Helper()
	claudeDir = t.TempDir()
	path := filepath.Join(claudeDir, "projects", "p", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	baseHash, err = sync.HashFile(path)
	if err != nil {
		t.Fatal(err)
	}

	state, err = sync.LoadStateFromDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seen = time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	state.UpdateFile(conflictedKey, info, baseHash)
	state.MarkRemoteSeen(conflictedKey, seen)

	if err := os.WriteFile(path, []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".conflict.20261116-080000", []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflicts, err = findConflicts(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("findConflicts returned %d conflicts, want 1", len(conflicts))
	}
	return claudeDir, state, conflicts, baseHash, seen
}

func assertNoBackslashKeys(t *testing.T, state *sync.SyncState) {
	t.Helper()
	for _, key := range state.Paths() {
		if strings.Contains(key, `\`) {
			t.Errorf("state holds a key with a backslash: %q", key)
		}
	}
}

func assertKeptLocal(t *testing.T, state *sync.SyncState, c conflictFile, baseHash string, seen time.Time) {
	t.Helper()
	if _, err := os.Stat(c.ConflictPath); !os.IsNotExist(err) {
		t.Errorf("conflict file still exists, stat error: %v", err)
	}
	got, err := os.ReadFile(c.OriginalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "local\n" {
		t.Errorf("local file = %q, want %q", got, "local\n")
	}
	entry := state.GetFile(conflictedKey)
	if entry == nil {
		t.Fatalf("state lost the entry for %s", conflictedKey)
	}
	if entry.Hash != baseHash {
		t.Errorf("state hash = %s, want the last synced hash %s so the next push uploads the kept version", entry.Hash, baseHash)
	}
	if !entry.Uploaded.Equal(seen) {
		t.Errorf("Uploaded = %v, want %v", entry.Uploaded, seen)
	}
	assertNoBackslashKeys(t, state)
}

func assertKeptRemote(t *testing.T, state *sync.SyncState, c conflictFile, seen time.Time) {
	t.Helper()
	got, err := os.ReadFile(c.OriginalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "remote\n" {
		t.Errorf("file = %q, want %q", got, "remote\n")
	}
	wantHash, err := sync.HashFile(c.OriginalPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := state.GetFile(conflictedKey)
	if entry == nil {
		t.Fatalf("state lost the entry for %s", conflictedKey)
	}
	if entry.Hash != wantHash {
		t.Errorf("state hash = %s, want the kept remote hash %s", entry.Hash, wantHash)
	}
	if !entry.Uploaded.Equal(seen) {
		t.Errorf("Uploaded = %v, want the storage time %v, not this machine's clock", entry.Uploaded, seen)
	}
	assertNoBackslashKeys(t, state)
}

func TestFindConflictsOffersOnlyDatedCopies(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.json", "a.json.conflict.20261116-080000", "notes.conflict.md"} {
		if err := os.WriteFile(filepath.Join(agents, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	conflicts, err := findConflicts(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(conflicts) != 1 {
		t.Fatalf("findConflicts returned %d entries, want 1: %+v", len(conflicts), conflicts)
	}
	if want := filepath.Join(agents, "a.json"); conflicts[0].OriginalPath != want {
		t.Errorf("OriginalPath = %q, want %q", conflicts[0].OriginalPath, want)
	}
	if conflicts[0].Timestamp != "20261116-080000" {
		t.Errorf("Timestamp = %q, want 20261116-080000", conflicts[0].Timestamp)
	}
}

func TestKeepLocalLeavesStateForThePush(t *testing.T) {
	claudeDir, state, conflicts, baseHash, seen := conflictFixture(t)

	if err := batchResolveConflicts(conflicts, "local", claudeDir, state); err != nil {
		t.Fatal(err)
	}

	assertKeptLocal(t, state, conflicts[0], baseHash, seen)
}

func TestKeepRemoteRecordsTheStorageTime(t *testing.T) {
	claudeDir, state, conflicts, _, seen := conflictFixture(t)

	if err := batchResolveConflicts(conflicts, "remote", claudeDir, state); err != nil {
		t.Fatal(err)
	}

	assertKeptRemote(t, state, conflicts[0], seen)
}

func TestInteractiveKeepLocalLeavesStateForThePush(t *testing.T) {
	claudeDir, state, conflicts, baseHash, seen := conflictFixture(t)

	if err := interactiveResolveConflicts(conflicts, claudeDir, state, strings.NewReader("l\n")); err != nil {
		t.Fatal(err)
	}

	assertKeptLocal(t, state, conflicts[0], baseHash, seen)
}

func TestInteractiveKeepRemoteRecordsTheStorageTime(t *testing.T) {
	claudeDir, state, conflicts, _, seen := conflictFixture(t)

	if err := interactiveResolveConflicts(conflicts, claudeDir, state, strings.NewReader("r\n")); err != nil {
		t.Fatal(err)
	}

	assertKeptRemote(t, state, conflicts[0], seen)
}

func firstContactFixture(t *testing.T) (claudeDir string, state *sync.SyncState, conflicts []conflictFile) {
	t.Helper()
	claudeDir = t.TempDir()
	path := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(path, []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".conflict.20261119-181816", []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	state, err = sync.LoadStateFromDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conflicts, err = findConflicts(claudeDir)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("findConflicts = %v, %v; want one conflict", conflicts, err)
	}
	return claudeDir, state, conflicts
}

func TestKeepLocalWithoutStateRegistersTheFileForThePush(t *testing.T) {
	claudeDir, state, conflicts := firstContactFixture(t)

	if err := batchResolveConflicts(conflicts, "local", claudeDir, state); err != nil {
		t.Fatal(err)
	}

	entry := state.GetFile("settings.json")
	if entry == nil {
		t.Fatal("a kept file with no state stays unregistered, so the push holds it forever")
	}
	if time.Since(entry.Uploaded) > time.Minute || entry.Uploaded.IsZero() {
		t.Errorf("Uploaded = %v, want about now so the push guard lets it through", entry.Uploaded)
	}
	changes, err := state.DetectChanges(claudeDir, []string{"settings.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != "modify" {
		t.Errorf("changes = %+v, want the kept file to read as modified so the push uploads it", changes)
	}
}

func TestKeepRemoteWithoutStateRecordsTheFile(t *testing.T) {
	claudeDir, state, conflicts := firstContactFixture(t)

	if err := batchResolveConflicts(conflicts, "remote", claudeDir, state); err != nil {
		t.Fatal(err)
	}

	entry := state.GetFile("settings.json")
	if entry == nil {
		t.Fatal("the kept remote file was not recorded as synced")
	}
	if entry.Uploaded.IsZero() {
		t.Error("Uploaded is zero, so every later push would hold the file as changed elsewhere")
	}
	changes, err := state.DetectChanges(claudeDir, []string{"settings.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %+v, want none after taking R2's version", changes)
	}
}
