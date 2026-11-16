package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/storage"
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

// A pull holds its deletions when they are more than deleteGuardFloor files and more than
// deleteGuardPercent of the tracked ones: that many at once means a wrong bucket, not a cleanup.
const (
	deleteGuardFloor   = 10
	deleteGuardPercent = 25
)

type deletionPlan struct {
	archive     []string        // gone from the bucket, unchanged here
	keptEdits   []string        // gone from the bucket, edited here: the next push uploads them again
	forget      []string        // gone from the bucket and from here
	deletedHere map[string]bool // gone from here, unchanged in the bucket since the last sync
	held        bool
	ran         bool // false when a key with an unknown path token made the listing untrustworthy
}

func (s *Syncer) deletionRulesApply(relPath string) bool {
	if relPath == HistoryFile || strings.HasPrefix(relPath, "_external/") ||
		strings.HasPrefix(relPath, "_metadata/") || s.isExcluded(relPath) {
		return false
	}
	for _, p := range s.syncPaths() {
		p = filepath.ToSlash(filepath.Clean(p))
		if relPath == p || strings.HasPrefix(relPath, p+"/") {
			return true
		}
	}
	return false
}

func (s *Syncer) planDeletions(remoteFiles map[string]storage.ObjectInfo, localFiles map[string]os.FileInfo, unknownTokens bool) deletionPlan {
	plan := deletionPlan{deletedHere: map[string]bool{}, ran: !unknownTokens}
	tracked := 0
	for _, relPath := range s.state.Paths() {
		st := s.state.GetFile(relPath)
		if st == nil || !s.deletionRulesApply(relPath) {
			continue
		}
		tracked++
		_, localExists := localFiles[relPath]
		remoteObj, inRemote := remoteFiles[relPath]
		switch {
		case inRemote && !localExists:
			if !remoteObj.LastModified.After(st.Uploaded) {
				plan.deletedHere[relPath] = true
			}
		case inRemote || unknownTokens:
		case !localExists:
			plan.forget = append(plan.forget, relPath)
		default:
			if hash, err := HashFile(filepath.Join(s.claudeDir, relPath)); err == nil && hash == st.Hash {
				plan.archive = append(plan.archive, relPath)
			} else {
				plan.keptEdits = append(plan.keptEdits, relPath)
			}
		}
	}
	sort.Strings(plan.archive)
	sort.Strings(plan.keptEdits)
	sort.Strings(plan.forget)
	plan.held = plan.ran && !s.allowDeletes && len(plan.archive) > deleteGuardFloor &&
		len(plan.archive)*100 > tracked*deleteGuardPercent
	return plan
}
