package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadRecordsStorageTime(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.store.clockAhead = -time.Hour // this machine runs an hour ahead of the storage
	enc, err := env.syncer.encryptor.Encrypt([]byte("remote"))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.Upload(ctx, "settings.json.age", enc); err != nil {
		t.Fatal(err)
	}
	if _, err := env.syncer.Pull(ctx); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	enc, err = env.syncer.encryptor.Encrypt([]byte("remote v2"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "settings.json.age", enc); err != nil {
		t.Fatal(err)
	}
	if _, err := env.syncer.Pull(ctx); err != nil {
		t.Fatalf("second Pull: %v", err)
	}

	if got := readFile(t, env.claudeDir, "settings.json"); got != "remote v2" {
		t.Errorf("settings.json = %q, want the second upload", got)
	}
}

func TestConflictCopyStaysOutOfState(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	writeFile(t, env.claudeDir, "settings.json", "base\n")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env.claudeDir, "settings.json", "base\nlocal\n")
	enc, err := env.syncer.encryptor.Encrypt([]byte("base\nremote\n"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "settings.json.age", enc); err != nil {
		t.Fatal(err)
	}

	result, err := env.syncer.Pull(ctx)
	if err != nil || len(result.Conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %v (err %v)", result.Conflicts, err)
	}
	for path := range env.syncer.state.Files {
		if conflictArtifactRe.MatchString(path) {
			t.Errorf("conflict copy %s recorded in the state", path)
		}
	}
	push, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(push.Deleted) != 0 {
		t.Errorf("push deleted %v, want nothing", push.Deleted)
	}
}

func TestArchiveKeepsFoldersAndNeverOverwrites(t *testing.T) {
	env := setupTestEnv(t)
	rel := "projects/p/memory/MEMORY.md"
	writeFile(t, env.claudeDir, rel, "one")
	if err := env.syncer.archiveDeleted(rel); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env.claudeDir, rel, "two")
	if err := env.syncer.archiveDeleted(rel); err != nil {
		t.Fatal(err)
	}

	day := filepath.Join(env.syncer.cfg.ArchiveDirOverride, time.Now().Format("20060102"), "deleted", "projects", "p", "memory")
	if got := readFile(t, day, "MEMORY.md"); got != "one" {
		t.Errorf("first archive = %q", got)
	}
	if got := readFile(t, day, "MEMORY.md.1"); got != "two" {
		t.Errorf("second archive = %q", got)
	}
	if _, err := os.Stat(filepath.Join(env.claudeDir, rel)); !os.IsNotExist(err) {
		t.Errorf("live file still there: %v", err)
	}
}
