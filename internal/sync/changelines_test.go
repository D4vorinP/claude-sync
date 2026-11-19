package sync

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPushSeparatesAddedUpdatedAndDeleted(t *testing.T) {
	env := trackedEnv(t, "agents/edited.json", "agents/gone.json")
	writeFile(t, env.claudeDir, "agents/edited.json", "edited")
	writeFile(t, env.claudeDir, "agents/new.json", "new")
	env.deleteLocal(t, "agents/gone.json")

	result, err := env.syncer.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "uploaded", result.Uploaded, "agents/edited.json", "agents/new.json")
	assertPaths(t, "updated", result.Updated, "agents/edited.json")
	assertPaths(t, "deleted", result.Deleted, "agents/gone.json")
}

func TestPullSeparatesAddedFromUpdated(t *testing.T) {
	env := trackedEnv(t, "agents/edited.json", "agents/same.json")
	ctx := context.Background()
	for rel, body := range map[string]string{"agents/edited.json": "edited elsewhere", "agents/new.json": "new elsewhere"} {
		enc, err := env.syncer.encryptor.Encrypt([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := env.store.Upload(ctx, env.syncer.remoteKey(rel), enc); err != nil {
			t.Fatal(err)
		}
	}

	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, "downloaded", result.Downloaded, "agents/edited.json", "agents/new.json")
	assertPaths(t, "updated", result.Updated, "agents/edited.json")
}

func TestChangeLinesGroupByVerbAndSortByPath(t *testing.T) {
	r := &SyncResult{
		Uploaded: []string{"b.json", "a.json", "c.json"},
		Updated:  []string{"c.json"},
		Deleted:  []string{"z.json", "y.json"},
	}
	want := []string{"added   a.json", "added   b.json", "updated c.json", "deleted y.json", "deleted z.json"}
	if got := r.ChangeLines(r.Uploaded); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ChangeLines = %q, want %q", got, want)
	}
}

func TestChangeLinesIsEmptyWhenNothingChanged(t *testing.T) {
	if got := (&SyncResult{}).ChangeLines(nil); len(got) != 0 {
		t.Errorf("ChangeLines = %q, want none", got)
	}
}
