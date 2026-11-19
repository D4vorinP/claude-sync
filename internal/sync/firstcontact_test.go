package sync

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func putRemote(t *testing.T, env *testEnv, rel, content string) {
	t.Helper()
	enc, err := env.syncer.encryptor.Encrypt([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.Upload(context.Background(), env.syncer.remoteKey(rel), enc); err != nil {
		t.Fatal(err)
	}
}

func remoteCopy(env *testEnv, rel string) []byte {
	return append([]byte(nil), env.store.objects[env.syncer.remoteKey(rel)].data...)
}

func TestPushHoldsBackAFileWithoutStateThatR2AlreadyHolds(t *testing.T) {
	env := setupTestEnv(t)
	putRemote(t, env, "settings.json", "shared settings")
	before := remoteCopy(env, "settings.json")
	time.Sleep(10 * time.Millisecond)
	writeFile(t, env.claudeDir, "settings.json", "a fresh machine's settings")
	writeFile(t, env.claudeDir, "agents/new.json", "new")

	result, err := env.syncer.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "held back", result.ChangedOnBoth, "settings.json")
	assertPaths(t, "uploaded", result.Uploaded, "agents/new.json")
	if !bytes.Equal(remoteCopy(env, "settings.json"), before) {
		t.Error("the copy in R2 was overwritten by a machine that had never synced it")
	}
}

func TestPullAdoptsAFileBothSidesHoldWithTheSameBytes(t *testing.T) {
	env := setupTestEnv(t)
	putRemote(t, env, "settings.json", "same on both")
	time.Sleep(10 * time.Millisecond)
	writeFile(t, env.claudeDir, "settings.json", "same on both")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "conflicts", result.Conflicts)
	assertPaths(t, "downloaded", result.Downloaded)
	entry := env.syncer.state.GetFile("settings.json")
	if entry == nil {
		t.Fatal("the identical file was not recorded as synced")
	}
	want := env.store.objects[env.syncer.remoteKey("settings.json")].lastModified
	if !entry.Uploaded.Equal(want) {
		t.Errorf("Uploaded = %v, want the storage time %v", entry.Uploaded, want)
	}

	push, err := env.syncer.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "uploaded", push.Uploaded)
	assertPaths(t, "held back", push.ChangedOnBoth)
}

func TestPullKeepsADifferingFileWithoutStateAndSavesR2AsConflict(t *testing.T) {
	env := setupTestEnv(t)
	putRemote(t, env, "settings.json", "shared settings")
	before := remoteCopy(env, "settings.json")
	time.Sleep(10 * time.Millisecond)
	writeFile(t, env.claudeDir, "settings.json", "a fresh machine's settings")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "conflicts", result.Conflicts, "settings.json")
	if got := readFile(t, env.claudeDir, "settings.json"); got != "a fresh machine's settings" {
		t.Errorf("local file = %q, want it kept", got)
	}
	copies, _ := filepath.Glob(filepath.Join(env.claudeDir, "settings.json.conflict.*"))
	if len(copies) != 1 {
		t.Fatalf("conflict copies = %v, want one", copies)
	}
	if got := readFile(t, filepath.Dir(copies[0]), filepath.Base(copies[0])); got != "shared settings" {
		t.Errorf("conflict copy = %q, want R2's version", got)
	}
	if env.syncer.state.GetFile("settings.json") != nil {
		t.Error("the file was recorded as synced although the two sides differ")
	}

	push, err := env.syncer.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "held back", push.ChangedOnBoth, "settings.json")
	if !bytes.Equal(remoteCopy(env, "settings.json"), before) {
		t.Error("the push after the conflict overwrote R2")
	}
}

func TestPullStillTakesR2OverAnOlderLocalFileWithoutState(t *testing.T) {
	env := setupTestEnv(t)
	writeFile(t, env.claudeDir, "settings.json", "older local default")
	time.Sleep(20 * time.Millisecond)
	putRemote(t, env, "settings.json", "shared settings")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "downloaded", result.Downloaded, "settings.json")
	if got := readFile(t, env.claudeDir, "settings.json"); got != "shared settings" {
		t.Errorf("local file = %q, want R2's version", got)
	}
}
