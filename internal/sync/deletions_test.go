package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// contentOf is valid JSONL, because history.jsonl goes through the history merge on push.
func contentOf(f string) string {
	return fmt.Sprintf("{\"display\":%q,\"timestamp\":1763280000000}\n", f)
}

func trackedEnv(t *testing.T, files ...string) *testEnv {
	t.Helper()
	env := setupTestEnv(t)
	for _, f := range files {
		writeFile(t, env.claudeDir, f, contentOf(f))
	}
	if _, err := env.syncer.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	return env
}

func (env *testEnv) plan(t *testing.T) deletionPlan {
	t.Helper()
	objs, _ := env.store.List(context.Background(), "")
	remote, skipped := env.syncer.buildRemoteMap(objs)
	local, err := GetLocalFiles(env.claudeDir, env.syncer.syncPaths(), env.syncer.isExcluded)
	if err != nil {
		t.Fatal(err)
	}
	return env.syncer.planDeletions(remote, local, len(skipped) > 0)
}

func (env *testEnv) deleteRemote(t *testing.T, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := env.store.Delete(context.Background(), env.syncer.remoteKey(rel)); err != nil {
			t.Fatal(err)
		}
	}
}

func (env *testEnv) deleteLocal(t *testing.T, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(env.claudeDir, rel)); err != nil {
		t.Fatal(err)
	}
}

func agentFiles(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("agents/f%02d.json", i)
	}
	return out
}

func assertPaths(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestPlanArchivesFileDeletedElsewhere(t *testing.T) {
	env := trackedEnv(t, "agents/a.json", "agents/b.json")
	env.deleteRemote(t, "agents/a.json")
	p := env.plan(t)
	assertPaths(t, "archive", p.archive, "agents/a.json")
	assertPaths(t, "keptEdits", p.keptEdits)
}

func TestPlanKeepsFileEditedHere(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteRemote(t, "agents/a.json")
	writeFile(t, env.claudeDir, "agents/a.json", "edited here")
	p := env.plan(t)
	assertPaths(t, "archive", p.archive)
	assertPaths(t, "keptEdits", p.keptEdits, "agents/a.json")
}

func TestPlanForgetsFileDeletedOnBothSides(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteRemote(t, "agents/a.json")
	env.deleteLocal(t, "agents/a.json")
	assertPaths(t, "forget", env.plan(t).forget, "agents/a.json")
}

func TestPlanDoesNotRestoreFileDeletedHere(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteLocal(t, "agents/a.json")
	if !env.plan(t).deletedHere["agents/a.json"] {
		t.Error("a file deleted here, unchanged in the bucket, must not be downloaded again")
	}
}

func TestPlanRestoresFileChangedElsewhereAfterLocalDelete(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteLocal(t, "agents/a.json")
	time.Sleep(10 * time.Millisecond)
	enc, _ := env.syncer.encryptor.Encrypt([]byte("edited elsewhere"))
	if err := env.store.Upload(context.Background(), env.syncer.remoteKey("agents/a.json"), enc); err != nil {
		t.Fatal(err)
	}
	if env.plan(t).deletedHere["agents/a.json"] {
		t.Error("an edit made elsewhere after the local delete must be downloaded")
	}
}

func TestPlanLeavesUntrackedClassesAlone(t *testing.T) {
	env := trackedEnv(t, "history.jsonl", "agents/skip/x.json", "agents/a.json")
	env.syncer.cfg.Exclude = []string{"agents/skip/**"}
	now := time.Now()
	env.syncer.state.Files["_external/mcp-servers.json"] = &FileState{Path: "_external/mcp-servers.json", Uploaded: now}
	env.syncer.state.Files["scripts/x.sh"] = &FileState{Path: "scripts/x.sh", Uploaded: now} // outside the default sync paths
	env.deleteRemote(t, "agents/skip/x.json")
	env.deleteLocal(t, "history.jsonl")

	p := env.plan(t)
	assertPaths(t, "archive", p.archive)
	assertPaths(t, "forget", p.forget)
	if p.deletedHere["history.jsonl"] {
		t.Error("a missing history.jsonl is always downloaded")
	}
}

func TestPlanSkipsGoneRowsOnUnknownToken(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteRemote(t, "agents/a.json")
	objs, _ := env.store.List(context.Background(), "")
	remote, _ := env.syncer.buildRemoteMap(objs)
	local, _ := GetLocalFiles(env.claudeDir, env.syncer.syncPaths(), env.syncer.isExcluded)

	p := env.syncer.planDeletions(remote, local, true)
	if p.ran || len(p.archive) != 0 {
		t.Errorf("with an unknown token the gone rows must not run: ran=%v archive=%v", p.ran, p.archive)
	}
}

func TestPlanHoldsMassDeletion(t *testing.T) {
	all := agentFiles(12)
	env := trackedEnv(t, all...)
	env.deleteRemote(t, all[:11]...)
	if !env.plan(t).held {
		t.Error("11 of 12 files gone must be held")
	}
	env.syncer.SetAllowDeletes(true)
	if env.plan(t).held {
		t.Error("--allow-deletes must lift the hold")
	}
}

func TestPlanFloorLetsSmallBatchThrough(t *testing.T) {
	all := agentFiles(3)
	env := trackedEnv(t, all...)
	env.deleteRemote(t, all[:2]...)
	if env.plan(t).held {
		t.Error("2 files are under the floor of 10")
	}
}

func TestPlanShareLetsRoutineCleanupThrough(t *testing.T) {
	all := agentFiles(60)
	env := trackedEnv(t, all...)
	env.deleteRemote(t, all[:11]...)
	if env.plan(t).held {
		t.Error("11 of 60 files is under 25%")
	}
}

func TestPlanNeverArchivesHistory(t *testing.T) {
	env := trackedEnv(t, "history.jsonl", "agents/a.json")
	env.deleteRemote(t, "history.jsonl")
	p := env.plan(t)
	assertPaths(t, "archive", p.archive)
	assertPaths(t, "keptEdits", p.keptEdits)
}

func TestPlanIgnoresConflictCopies(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	const copyPath = "agents/a.json.conflict.20261116-091426"
	env.syncer.state.Files[copyPath] = &FileState{Path: copyPath, Uploaded: time.Now()}
	writeFile(t, env.claudeDir, copyPath, "remote version")
	p := env.plan(t)
	assertPaths(t, "archive", p.archive)
	assertPaths(t, "keptEdits", p.keptEdits)
	assertPaths(t, "forget", p.forget)
}

func TestPlanHoldBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		n, k int
		held bool
	}{
		{"ten files are not more than the floor", 12, 10, false},
		{"exactly a quarter is not more than a quarter", 44, 11, false},
		{"just over a quarter is held", 43, 11, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			all := agentFiles(c.n)
			env := trackedEnv(t, all...)
			env.deleteRemote(t, all[:c.k]...)
			if got := env.plan(t).held; got != c.held {
				t.Errorf("%d of %d gone: held = %v, want %v", c.k, c.n, got, c.held)
			}
		})
	}
}

func TestPlanMatchesSyncPathWithTrailingSlash(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.syncer.cfg.SyncPaths = []string{"agents/"}
	env.deleteRemote(t, "agents/a.json")
	assertPaths(t, "archive", env.plan(t).archive, "agents/a.json")
}

func TestPlanSyncPathDoesNotMatchSiblingPrefix(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.syncer.cfg.SyncPaths = []string{"agents"}
	env.syncer.state.Files["agentsX/b.json"] = &FileState{Path: "agentsX/b.json", Uploaded: time.Now()}
	assertPaths(t, "forget", env.plan(t).forget)
}

func TestPullArchivesFileDeletedElsewhere(t *testing.T) {
	env := trackedEnv(t, "agents/a.json", "agents/b.json")
	env.deleteRemote(t, "agents/a.json")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "archived", result.Archived, "agents/a.json")
	if env.syncer.state.GetFile("agents/a.json") != nil {
		t.Error("archived file still in the state")
	}
	day := filepath.Join(env.syncer.cfg.ArchiveDirOverride, time.Now().Format("20060102"), "deleted", "agents")
	if got := readFile(t, day, "a.json"); got != contentOf("agents/a.json") {
		t.Errorf("archive holds %q", got)
	}
}

func TestPullDoesNotRestoreDeletionMadeHere(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteLocal(t, "agents/a.json")
	ctx := context.Background()

	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "downloaded", result.Downloaded)
	push, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "push deleted", push.Deleted, "agents/a.json")
}

func TestPullRestoresMissingHistory(t *testing.T) {
	env := trackedEnv(t, "history.jsonl")
	env.deleteLocal(t, "history.jsonl")
	if _, err := env.syncer.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.claudeDir, "history.jsonl")); err != nil {
		t.Errorf("history.jsonl not restored: %v", err)
	}
}

func TestPullHoldsMassDeletionUntilAllowed(t *testing.T) {
	all := agentFiles(12)
	env := trackedEnv(t, all...)
	env.deleteRemote(t, all[:11]...)
	ctx := context.Background()
	marker := filepath.Join(env.stateDir, HeldMarkerFile)

	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Held) != 11 || len(result.Archived) != 0 {
		t.Fatalf("held %d, archived %d; want 11 and 0", len(result.Held), len(result.Archived))
	}
	var held HeldDeletions
	data, err := os.ReadFile(marker)
	if err != nil || json.Unmarshal(data, &held) != nil || held.Count != 11 {
		t.Fatalf("marker = %s (err %v)", data, err)
	}
	if held.Time.Nanosecond() != 0 {
		t.Errorf("marker time %s has a fractional second", held.Time.Format(time.RFC3339Nano))
	}

	env.syncer.SetAllowDeletes(true)
	result, err = env.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Archived) != 11 {
		t.Errorf("archived %d after --allow-deletes, want 11", len(result.Archived))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker must go once nothing is held: %v", err)
	}
}

func TestMarkerStaysWhenTheGoneRowsDidNotRun(t *testing.T) {
	env := setupTestEnv(t)
	marker := filepath.Join(env.stateDir, HeldMarkerFile)
	writeFile(t, env.stateDir, HeldMarkerFile, `{"count":11}`)

	env.syncer.applyDeletions(deletionPlan{ran: false}, &SyncResult{})
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a pull that skipped the gone rows must leave the marker: %v", err)
	}
}

func TestPullKeepsStateWhenArchiveFails(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	env.deleteRemote(t, "agents/a.json")
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	writeFile(t, filepath.Dir(blocker), filepath.Base(blocker), "x")
	env.syncer.cfg.ArchiveDirOverride = blocker

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) == 0 || len(result.Archived) != 0 {
		t.Errorf("errors %v, archived %v; want an error and nothing archived", result.Errors, result.Archived)
	}
	if env.syncer.state.GetFile("agents/a.json") == nil {
		t.Error("a file that failed to archive must stay in the state")
	}
}

func TestPullKeepsFileRenamedByCaseOnly(t *testing.T) {
	env := trackedEnv(t, "agents/a.json", "agents/b.json")
	enc, err := env.syncer.encryptor.Encrypt([]byte("renamed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.Upload(context.Background(), env.syncer.remoteKey("agents/A.json"), enc); err != nil {
		t.Fatal(err)
	}
	env.deleteRemote(t, "agents/a.json")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "archived", result.Archived)
	if _, err := os.Stat(filepath.Join(env.claudeDir, "agents", "a.json")); err != nil {
		t.Errorf("the file renamed by case only must stay: %v", err)
	}
}

func TestApplyDeletionsKeepsFileEditedSincePlan(t *testing.T) {
	env := trackedEnv(t, "agents/a.json")
	plan := deletionPlan{ran: true, archive: []string{"agents/a.json"}, deletedHere: map[string]bool{}}
	writeFile(t, env.claudeDir, "agents/a.json", "edited meanwhile")

	var result SyncResult
	env.syncer.applyDeletions(plan, &result)

	assertPaths(t, "archived", result.Archived)
	assertPaths(t, "keptEdits", result.KeptEdits, "agents/a.json")
	if got := readFile(t, env.claudeDir, "agents/a.json"); got != "edited meanwhile" {
		t.Errorf("local file = %q", got)
	}
	if env.syncer.state.GetFile("agents/a.json") == nil {
		t.Error("an edited file must stay in the state")
	}
}

func TestPullForgetsDeletedOnBothSidesAndKeepsLocalEdits(t *testing.T) {
	env := trackedEnv(t, "agents/a.json", "agents/c.json", "agents/d.json")
	env.deleteRemote(t, "agents/c.json", "agents/d.json")
	env.deleteLocal(t, "agents/c.json")
	writeFile(t, env.claudeDir, "agents/d.json", "edited here")

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if env.syncer.state.GetFile("agents/c.json") != nil {
		t.Error("a file deleted on both sides must leave the state")
	}
	assertPaths(t, "keptEdits", result.KeptEdits, "agents/d.json")
	if env.syncer.state.GetFile("agents/d.json") == nil {
		t.Error("an edited file must stay in the state")
	}
}

func TestPreviewMatchesPull(t *testing.T) {
	env := trackedEnv(t, "agents/a.json", "agents/b.json", "agents/c.json")
	env.deleteRemote(t, "agents/a.json")
	env.deleteLocal(t, "agents/b.json")
	ctx := context.Background()

	preview, err := env.syncer.PreviewPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pathsOf := func(fs []FilePreview) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Path)
		}
		return out
	}
	assertPaths(t, "would archive", pathsOf(preview.WouldArchive), "agents/a.json")
	assertPaths(t, "would not restore", pathsOf(preview.WouldNotRestore), "agents/b.json")
	assertPaths(t, "would download", pathsOf(preview.WouldDownload))

	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "archived", result.Archived, "agents/a.json")
	assertPaths(t, "downloaded", result.Downloaded)
}

func TestPreviewOnEmptyBucketPlansNoDeletions(t *testing.T) {
	env := trackedEnv(t, agentFiles(12)...)
	ctx := context.Background()
	objs, err := env.store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if err := env.store.Delete(ctx, o.Key); err != nil {
			t.Fatal(err)
		}
	}

	preview, err := env.syncer.PreviewPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.WouldArchive) != 0 || len(preview.KeptEdits) != 0 || preview.DeletionsHeld {
		t.Errorf("an empty bucket plans no deletions, got archive %d, kept edits %d, held %v",
			len(preview.WouldArchive), len(preview.KeptEdits), preview.DeletionsHeld)
	}
	if len(preview.LocalOnlyFiles) != 12 {
		t.Errorf("local-only files = %d, want 12", len(preview.LocalOnlyFiles))
	}
}

func TestPreviewHoldsWithoutTouchingAnything(t *testing.T) {
	files := agentFiles(12)
	env := trackedEnv(t, files...)
	env.deleteRemote(t, files[:11]...)
	pathsBefore := env.syncer.state.Paths()
	sort.Strings(pathsBefore)

	preview, err := env.syncer.PreviewPull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DeletionsHeld || len(preview.WouldArchive) != 11 {
		t.Fatalf("held = %v, would archive %d, want true and 11", preview.DeletionsHeld, len(preview.WouldArchive))
	}

	for _, f := range files[:11] {
		if _, err := os.Stat(filepath.Join(env.claudeDir, f)); err != nil {
			t.Errorf("%s must still exist: %v", f, err)
		}
	}
	pathsAfter := env.syncer.state.Paths()
	sort.Strings(pathsAfter)
	if fmt.Sprint(pathsAfter) != fmt.Sprint(pathsBefore) {
		t.Errorf("state paths changed: %v, was %v", pathsAfter, pathsBefore)
	}
	if _, err := os.Stat(filepath.Join(env.stateDir, HeldMarkerFile)); !os.IsNotExist(err) {
		t.Errorf("a preview wrote the held marker: %v", err)
	}
	if entries, _ := os.ReadDir(env.syncer.cfg.ArchiveDirOverride); len(entries) != 0 {
		t.Errorf("a preview archived %d entries", len(entries))
	}
}

func TestPreviewShowsKeptEdits(t *testing.T) {
	env := trackedEnv(t, "agents/a.json", "agents/b.json")
	env.deleteRemote(t, "agents/a.json")
	writeFile(t, env.claudeDir, "agents/a.json", "edited here")

	preview, err := env.syncer.PreviewPull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.KeptEdits) != 1 || preview.KeptEdits[0].Path != "agents/a.json" {
		t.Errorf("kept edits = %v, want agents/a.json", preview.KeptEdits)
	}
	if len(preview.WouldArchive) != 0 {
		t.Errorf("an edited file must not be archived, got %v", preview.WouldArchive)
	}
}

func TestPushKeepsFileChangedElsewhereAfterLocalDelete(t *testing.T) {
	env := trackedEnv(t, "agents/x.json", "agents/y.json")
	ctx := context.Background()
	env.deleteLocal(t, "agents/x.json")
	env.deleteLocal(t, "agents/y.json")
	time.Sleep(10 * time.Millisecond)
	enc, err := env.syncer.encryptor.Encrypt([]byte("edited on the other machine"))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.Upload(ctx, env.syncer.remoteKey("agents/x.json"), enc); err != nil {
		t.Fatal(err)
	}

	push, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "push deleted", push.Deleted, "agents/y.json")
	assertPaths(t, "changed elsewhere", push.ChangedElsewhere, "agents/x.json")

	if _, err := env.syncer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, env.claudeDir, "agents/x.json"); got != "edited on the other machine" {
		t.Errorf("x.json = %q, want the other machine's edit back", got)
	}
}
