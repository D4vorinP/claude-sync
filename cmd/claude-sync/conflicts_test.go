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

func TestKeepLocalLeavesStateForThePush(t *testing.T) {
	claudeDir, state, conflicts, baseHash, seen := conflictFixture(t)

	if err := batchResolveConflicts(conflicts, "local", claudeDir, state); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(conflicts[0].ConflictPath); !os.IsNotExist(err) {
		t.Errorf("conflict file still exists, stat error: %v", err)
	}
	got, err := os.ReadFile(conflicts[0].OriginalPath)
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

func TestKeepRemoteRecordsTheStorageTime(t *testing.T) {
	claudeDir, state, conflicts, _, seen := conflictFixture(t)

	if err := batchResolveConflicts(conflicts, "remote", claudeDir, state); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(conflicts[0].OriginalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "remote\n" {
		t.Errorf("file = %q, want %q", got, "remote\n")
	}
	wantHash, err := sync.HashFile(conflicts[0].OriginalPath)
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
