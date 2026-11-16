package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
)

func (s *Syncer) archiveRoot() (string, error) {
	if s.cfg.ArchiveDirOverride != "" {
		return s.cfg.ArchiveDirOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", config.ErrNoHomeDir
	}
	return filepath.Join(home, ".claude-sync-archive"), nil
}

// archiveDeleted moves a file the other machine deleted out of ~/.claude. A rename only: a file
// Claude Code holds open fails here, stays tracked, and the next pull tries again.
func (s *Syncer) archiveDeleted(relPath string) error {
	root, err := s.archiveRoot()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, time.Now().Format("20060102"), "deleted", filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	target := dst
	for n := 1; ; n++ {
		if _, err := os.Lstat(target); err != nil {
			break
		}
		target = fmt.Sprintf("%s.%d", dst, n)
	}
	return os.Rename(filepath.Join(s.claudeDir, filepath.FromSlash(relPath)), target)
}
