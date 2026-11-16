package sync

import (
	"encoding/json"
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
	remoteFold := make(map[string]bool, len(remoteFiles))
	for k := range remoteFiles {
		remoteFold[strings.ToLower(k)] = true
	}
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
		// A case-only rename elsewhere: on Windows both names are this one file.
		case inRemote || unknownTokens || remoteFold[strings.ToLower(relPath)]:
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

const HeldMarkerFile = "deletions-held.json"

type HeldDeletions struct {
	Time  time.Time `json:"time"`
	Count int       `json:"count"`
	Paths []string  `json:"paths"`
}

func (s *Syncer) heldMarkerPath() string {
	return filepath.Join(s.state.Dir(), HeldMarkerFile)
}

func (s *Syncer) applyDeletions(plan deletionPlan, result *SyncResult) {
	if !plan.ran {
		return
	}
	for _, p := range plan.forget {
		s.state.RemoveFile(p)
	}
	result.KeptEdits = plan.keptEdits
	if plan.held {
		result.Held = plan.archive
		data, _ := json.MarshalIndent(HeldDeletions{Time: time.Now(), Count: len(plan.archive), Paths: plan.archive}, "", "  ")
		if err := os.WriteFile(s.heldMarkerPath(), data, 0600); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("writing the held-deletions marker: %w", err))
		}
		return
	}
	for _, p := range plan.archive {
		// The plan hashed before the downloads; an edit made since then must not be archived.
		hash, err := HashFile(filepath.Join(s.claudeDir, filepath.FromSlash(p)))
		if st := s.state.GetFile(p); err != nil || st == nil || hash != st.Hash {
			result.KeptEdits = append(result.KeptEdits, p)
			continue
		}
		if err := s.archiveDeleted(p); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: archiving a file deleted elsewhere: %w", p, err))
			continue
		}
		s.state.RemoveFile(p)
		result.Archived = append(result.Archived, p)
	}
	if err := os.Remove(s.heldMarkerPath()); err != nil && !os.IsNotExist(err) {
		result.Errors = append(result.Errors, fmt.Errorf("removing the held-deletions marker: %w", err))
	}
}
