package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
)

// gzipCompress compresses a payload the way storage format 1 did, for tests that
// need a format-1 object in the bucket.
func gzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// uploadPlaintext encrypts payload and stores it under key, bypassing Push, so a
// test can put an object in the bucket exactly as an older build wrote it.
func uploadPlaintext(t *testing.T, env *testEnv, key string, payload []byte) {
	t.Helper()
	encrypted, err := env.syncer.encryptor.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt %s: %v", key, err)
	}
	if err := env.store.Upload(context.Background(), key, encrypted); err != nil {
		t.Fatalf("upload %s: %v", key, err)
	}
}

// plaintextOf returns the object under key decrypted but still compressed.
func plaintextOf(t *testing.T, env *testEnv, key string) []byte {
	t.Helper()
	encrypted, err := env.store.Download(context.Background(), key)
	if err != nil {
		t.Fatalf("download %s: %v", key, err)
	}
	data, err := env.syncer.encryptor.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("decrypt %s: %v", key, err)
	}
	return data
}

func TestPushUploadsZstd(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	env.syncer.cfg.ClaudeJSONOverride = filepath.Join(t.TempDir(), "claude.json")
	writeFile(t, filepath.Dir(env.syncer.cfg.ClaudeJSONOverride), "claude.json",
		`{"mcpServers":{"test-server":{"command":"node"}}}`)
	writeFile(t, env.claudeDir, "CLAUDE.md", "# zstd")

	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if _, err := env.syncer.PushMCP(ctx); err != nil {
		t.Fatalf("PushMCP failed: %v", err)
	}

	for _, key := range []string{"CLAUDE.md.age", ManifestKey + ".age", config.MCPRemoteKey + ".age"} {
		if p := plaintextOf(t, env, key); !isZstd(p) {
			t.Errorf("%s: expected a zstd payload, got % x", key, p[:min(4, len(p))])
		}
	}

	manifest, err := env.syncer.downloadManifest(ctx)
	if err != nil || manifest == nil {
		t.Fatalf("downloadManifest: manifest %v, err %v", manifest, err)
	}
	if _, ok := manifest.Files["CLAUDE.md"]; !ok {
		t.Errorf("manifest read back without CLAUDE.md: %v", manifest.Files)
	}
}

// A file that has not changed since a format-1 build uploaded it stays in the
// bucket as gzip, or uncompressed from before compression existed.
func TestPullReadsFormatOneObjects(t *testing.T) {
	env := setupTestEnv(t)

	gz, err := gzipCompress([]byte("# gzip"))
	if err != nil {
		t.Fatal(err)
	}
	uploadPlaintext(t, env, "CLAUDE.md.age", gz)
	uploadPlaintext(t, env, "settings.json.age", []byte(`{"v":"uncompressed"}`))

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("Pull errors: %v", result.Errors)
	}
	if got := readFile(t, env.claudeDir, "CLAUDE.md"); got != "# gzip" {
		t.Errorf("gzip object: got %q", got)
	}
	if got := readFile(t, env.claudeDir, "settings.json"); got != `{"v":"uncompressed"}` {
		t.Errorf("uncompressed object: got %q", got)
	}
}

func TestDecompressPayloadHonoursSizeLimit(t *testing.T) {
	prev := maxDecompressedSize
	maxDecompressedSize = 64 << 10
	t.Cleanup(func() { maxDecompressedSize = prev })

	if _, err := decompressPayload(compressPayload(bytes.Repeat([]byte("a"), 32<<10))); err != nil {
		t.Errorf("payload under the limit rejected: %v", err)
	}
	if _, err := decompressPayload(compressPayload(bytes.Repeat([]byte("a"), 128<<10))); err == nil {
		t.Error("payload over the limit was decompressed")
	}
}
