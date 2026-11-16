package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
)

func useStorageFormat(t *testing.T, n int) {
	t.Helper()
	prev := storageFormat
	storageFormat = n
	t.Cleanup(func() { storageFormat = prev })
}

// secondDevice returns a syncer with its own empty claude dir and state that
// shares env's bucket and key, like a second machine on the same account.
func secondDevice(t *testing.T, env *testEnv) (*Syncer, string) {
	t.Helper()
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	stateDir := filepath.Join(root, ".claude-sync")
	for _, d := range []string{claudeDir, stateDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("Failed to create %s: %v", d, err)
		}
	}
	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}
	return &Syncer{
		storage:   env.store,
		encryptor: env.syncer.encryptor,
		state:     state,
		claudeDir: claudeDir,
		quiet:     true,
		cfg:       &config.Config{ArchiveDirOverride: filepath.Join(root, "archive")},
	}, claudeDir
}

// The MCP config and a path migration are written outside Push and read outside
// Pull, so they must pass the same gate.
func TestFormatGateCoversMCPAndMigrate(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	older, _ := secondDevice(t, env)
	for _, s := range []*Syncer{env.syncer, older} {
		s.cfg.ClaudeJSONOverride = filepath.Join(t.TempDir(), "claude.json")
		writeFile(t, filepath.Dir(s.cfg.ClaudeJSONOverride), "claude.json",
			`{"mcpServers":{"test-server":{"command":"node"}}}`)
	}

	if _, err := env.syncer.PushMCP(ctx); err != nil {
		t.Fatalf("PushMCP failed: %v", err)
	}
	if _, err := env.store.Head(ctx, FormatKey+".age"); err != nil {
		t.Fatalf("An MCP push on an unmarked bucket must write the format marker: %v", err)
	}

	useStorageFormat(t, 1)
	var tooNew *FormatTooNewError

	if _, err := older.PullMCP(ctx); !errors.As(err, &tooNew) {
		t.Errorf("older device PullMCP: expected FormatTooNewError, got %v", err)
	}
	before, _ := env.store.Head(ctx, config.MCPRemoteKey+".age")
	if _, err := older.PushMCP(ctx); !errors.As(err, &tooNew) {
		t.Errorf("older device PushMCP: expected FormatTooNewError, got %v", err)
	}
	if after, _ := env.store.Head(ctx, config.MCPRemoteKey+".age"); !after.LastModified.Equal(before.LastModified) {
		t.Error("A halted MCP push must not upload anything")
	}
	if _, err := older.MigratePaths(ctx); !errors.As(err, &tooNew) {
		t.Errorf("older device MigratePaths: expected FormatTooNewError, got %v", err)
	}
}

func TestPushLeavesUnmarkedBucketUnmarked(t *testing.T) {
	useStorageFormat(t, 1)
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if _, err := env.store.Head(ctx, FormatKey+".age"); err == nil {
		t.Error("A push on the bucket's own format must not write a format marker")
	}
}

func TestNewerFormatPushHaltsOlderDevice(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	older, olderDir := secondDevice(t, env)

	writeFile(t, older.claudeDir, "settings.json", `{"v":"older"}`)
	if _, err := older.Push(ctx); err != nil {
		t.Fatalf("older device push before the upgrade failed: %v", err)
	}

	useStorageFormat(t, 2)
	writeFile(t, env.claudeDir, "CLAUDE.md", "# from the upgraded device")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("upgraded device push failed: %v", err)
	}
	if _, err := env.store.Head(ctx, FormatKey+".age"); err != nil {
		t.Fatalf("Expected the upgraded push to write the format marker: %v", err)
	}

	useStorageFormat(t, 1)
	var tooNew *FormatTooNewError

	_, err := older.Pull(ctx)
	if !errors.As(err, &tooNew) {
		t.Fatalf("older device pull: expected FormatTooNewError, got %v", err)
	}
	if tooNew.Bucket != 2 || tooNew.Binary != 1 {
		t.Errorf("Expected bucket 2 / binary 1, got bucket %d / binary %d", tooNew.Bucket, tooNew.Binary)
	}
	if _, err := os.Stat(filepath.Join(olderDir, "CLAUDE.md")); err == nil {
		t.Error("A halted pull must not download anything")
	}

	writeFile(t, older.claudeDir, "settings.json", `{"v":"older-2"}`)
	before, _ := env.store.Head(ctx, "settings.json.age")
	if _, err := older.Push(ctx); !errors.As(err, &tooNew) {
		t.Fatalf("older device push: expected FormatTooNewError, got %v", err)
	}
	after, _ := env.store.Head(ctx, "settings.json.age")
	if !after.LastModified.Equal(before.LastModified) {
		t.Error("A halted push must not upload anything")
	}

	useStorageFormat(t, 2)
	if _, err := older.Pull(ctx); err != nil {
		t.Fatalf("pull after upgrading the older device failed: %v", err)
	}
	if got := readFile(t, olderDir, "CLAUDE.md"); got != "# from the upgraded device" {
		t.Errorf("Expected the upgraded device's file after upgrading, got %q", got)
	}
}
