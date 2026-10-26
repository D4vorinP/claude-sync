package sync

import (
	"context"
	"testing"
)

// Content that only looks like a path token must reach the other device as it was
// written, while real paths still translate.
func TestEncodeDecodeContentKeepsLiteralDollars(t *testing.T) {
	alice := mustMapper(t, `C:\Users\alice`, map[string]string{`\\srv\c$`: "SHARE"})
	bob := mustMapper(t, `C:\Users\bob`, map[string]string{`\\nas\d$`: "SHARE"})

	cases := []struct{ name, in, want string }{
		{"literal token", `{"a":"${HOME}\\.claude"}`, `{"a":"${HOME}\\.claude"}`},
		{"literal escaped token", `{"a":"$${SHARE}"}`, `{"a":"$${SHARE}"}`},
		{"dollar before a path", `{"a":"$C:\\Users\\alice\\x"}`, `{"a":"$C:\\Users\\bob\\x"}`},
		{"dollars elsewhere", `{"a":"$$ $env:X ${env:Y} $"}`, `{"a":"$$ $env:X ${env:Y} $"}`},
		{"path", `{"a":"C:\\Users\\alice\\app"}`, `{"a":"C:\\Users\\bob\\app"}`},
		{"mapped path holding a dollar", `{"a":"\\\\srv\\c$\\data"}`, `{"a":"\\\\nas\\d$\\data"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := alice.EncodeContent(sessionFile, []byte(tc.in))
			if got := string(bob.DecodeContent(sessionFile, remote)); got != tc.want {
				t.Errorf("remote %s\n got  %s\n want %s", remote, got, tc.want)
			}
		})
	}
}

func TestEncodeDecodeContentWithoutMappings(t *testing.T) {
	in := `$${X} $ ${HOME} ${}`
	for name, m := range map[string]*PathMapper{"nil mapper": nil, "no mappings": mustMapper(t, "", nil)} {
		if got := string(m.DecodeContent(sessionFile, m.EncodeContent(sessionFile, []byte(in)))); got != in {
			t.Errorf("%s: got %s, want %s", name, got, in)
		}
	}
}

func TestLiteralTokenSurvivesSync(t *testing.T) {
	alice := setupTestEnv(t)
	alice.syncer.paths = mustMapper(t, "/Users/alice", nil)
	bob, bobDir := secondDevice(t, alice)
	bob.paths = mustMapper(t, "/Users/bob", nil)
	ctx := context.Background()

	writeFile(t, alice.claudeDir, "projects/-Users-alice-app/s.jsonl",
		`{"cmd":"ls ${HOME}/.claude $${HOME}","cwd":"/Users/alice/app"}`+"\n")
	writeFile(t, alice.claudeDir, "history.jsonl", `{"display":"echo ${HOME}"}`+"\n")
	if _, err := alice.syncer.Push(ctx); err != nil {
		t.Fatalf("Push: %v", err)
	}
	result, err := bob.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("Pull errors: %v", result.Errors)
	}

	if got, want := readFile(t, bobDir, "projects/-Users-bob-app/s.jsonl"),
		`{"cmd":"ls ${HOME}/.claude $${HOME}","cwd":"/Users/bob/app"}`+"\n"; got != want {
		t.Errorf("session\n got  %s want %s", got, want)
	}
	if got, want := readFile(t, bobDir, "history.jsonl"), `{"display":"echo ${HOME}"}`+"\n"; got != want {
		t.Errorf("history\n got  %s want %s", got, want)
	}
}

// A format-1 object did not escape its dollars, so it must pull exactly as before.
func TestFormatOneObjectResolvesAsBefore(t *testing.T) {
	env := setupTestEnv(t)
	env.syncer.paths = mustMapper(t, "/Users/bob", nil)

	gz, err := gzipCompress([]byte(`{"cwd":"${HOME}/app","note":"$${HOME}"}`))
	if err != nil {
		t.Fatal(err)
	}
	uploadPlaintext(t, env, "projects/${HOME}-app/s.jsonl.age", gz)

	if _, err := env.syncer.Pull(context.Background()); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got, want := readFile(t, env.claudeDir, "projects/-Users-bob-app/s.jsonl"),
		`{"cwd":"/Users/bob/app","note":"$/Users/bob"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
