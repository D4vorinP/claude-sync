package sync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tawanorg/claude-sync/internal/storage"
)

// FormatKey is the remote storage key for the bucket's storage format marker.
const FormatKey = "_metadata/format.json"

// storageFormat is the bucket format this build reads and writes. Raise it only when
// an older build would misread what this one uploads. A var so tests can act as one.
var storageFormat = 2

// FormatTooNewError stops a sync because the bucket holds a format this build would
// misread.
type FormatTooNewError struct {
	Bucket int
	Binary int
}

func (e *FormatTooNewError) Error() string {
	return fmt.Sprintf("sync halted: the bucket uses storage format %d, this claude-sync understands up to %d; upgrade claude-sync on this machine",
		e.Bucket, e.Binary)
}

type formatMarker struct {
	Format int `json:"format"`
}

// checkFormat halts on a bucket newer than this build. Before writing, it raises an
// older bucket's marker first, so an older build halts instead of misreading uploads.
func (s *Syncer) checkFormat(ctx context.Context, listing []storage.ObjectInfo, writing bool) error {
	current, err := s.bucketFormat(ctx, listing)
	if err != nil {
		return fmt.Errorf("failed to read storage format: %w", err)
	}
	if current > storageFormat {
		return &FormatTooNewError{Bucket: current, Binary: storageFormat}
	}
	if writing && current < storageFormat {
		if err := s.writeFormat(ctx); err != nil {
			return fmt.Errorf("failed to write storage format: %w", err)
		}
	}
	return nil
}

// bucketFormat reads the marker if listing holds it. A bucket without one predates
// the marker and is format 1.
func (s *Syncer) bucketFormat(ctx context.Context, listing []storage.ObjectInfo) (int, error) {
	present := false
	for _, obj := range listing {
		if obj.Key == FormatKey+".age" {
			present = true
			break
		}
	}
	if !present {
		return 1, nil
	}

	encrypted, err := s.storage.Download(ctx, FormatKey+".age")
	if err != nil {
		return 0, err
	}
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return 0, err
	}
	var m formatMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return 0, err
	}
	if m.Format < 1 {
		return 0, fmt.Errorf("invalid storage format %d", m.Format)
	}
	return m.Format, nil
}

func (s *Syncer) writeFormat(ctx context.Context) error {
	data, err := json.Marshal(formatMarker{Format: storageFormat})
	if err != nil {
		return err
	}
	encrypted, err := s.encryptor.Encrypt(data)
	if err != nil {
		return err
	}
	return s.storage.Upload(ctx, FormatKey+".age", encrypted)
}
