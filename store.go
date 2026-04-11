package tq

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type workspaceConfig struct {
	Version  int    `json:"version"`
	IDPrefix string `json:"id_prefix"`
}

// Store manages a .tq workspace.
type Store struct {
	tqDir   string
	prefix  string
	now     func() time.Time
}

// Init creates a new .tq workspace at dir.
func Init(dir string, prefix string) error {
	if err := validatePrefix(prefix); err != nil {
		return err
	}

	tqDir := filepath.Join(dir, ".tq")
	if _, err := os.Stat(tqDir); err == nil {
		return ErrAlreadyInit
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat workspace dir: %w", err)
	}

	issuesDir := filepath.Join(tqDir, "issues")
	if err := os.MkdirAll(issuesDir, 0o755); err != nil {
		return fmt.Errorf("create workspace dirs: %w", err)
	}

	cfg := workspaceConfig{Version: 1, IDPrefix: prefix}
	cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	cfgBytes = append(cfgBytes, '\n')
	if err := os.WriteFile(filepath.Join(tqDir, "config.json"), cfgBytes, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	gitignore := []byte("lock\nissues/*.tmp\n")
	if err := os.WriteFile(filepath.Join(tqDir, ".gitignore"), gitignore, 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	return nil
}

// Open opens an existing .tq workspace at dir.
func Open(dir string) (*Store, error) {
	tqDir := filepath.Join(dir, ".tq")
	st, err := os.Stat(tqDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("stat workspace dir: %w", err)
	}
	if !st.IsDir() {
		return nil, ErrNotInitialized
	}

	cfg, err := readConfig(filepath.Join(tqDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := validatePrefix(cfg.IDPrefix); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &Store{
		tqDir:  tqDir,
		prefix: cfg.IDPrefix,
		now:    time.Now,
	}, nil
}

func newStoreForTest(tqDir string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	s := &Store{tqDir: tqDir, now: now}
	cfg, err := readConfig(filepath.Join(tqDir, "config.json"))
	if err == nil {
		s.prefix = cfg.IDPrefix
	}
	return s
}

func readConfig(path string) (*workspaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg workspaceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported config version: %d", cfg.Version)
	}
	return &cfg, nil
}

func (s *Store) issuesDir() string {
	return filepath.Join(s.tqDir, "issues")
}

func (s *Store) issuePath(id string) string {
	return filepath.Join(s.issuesDir(), id+".json")
}

func (s *Store) readIssue(id string) (*Issue, error) {
	path := s.issuePath(id)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read issue %s: %w", id, err)
	}

	var issue Issue
	if err := json.Unmarshal(data, &issue); err != nil {
		return nil, fmt.Errorf("parse issue %s: %w", id, ErrCorruptFile)
	}
	if issue.ID != id {
		return nil, fmt.Errorf("issue id mismatch for %s: %w", id, ErrCorruptFile)
	}

	return &issue, nil
}

func (s *Store) writeIssue(issue *Issue) error {
	if issue == nil {
		return fmt.Errorf("issue is nil")
	}

	finalPath := s.issuePath(issue.ID)
	tmpPath := finalPath + ".tmp"

	payload, err := json.MarshalIndent(issue, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal issue %s: %w", issue.ID, err)
	}
	payload = append(payload, '\n')

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open temp file for %s: %w", issue.ID, err)
	}

	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file for %s: %w", issue.ID, err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync temp file for %s: %w", issue.ID, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file for %s: %w", issue.ID, err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename temp file for %s: %w", issue.ID, err)
	}

	dir, err := os.Open(s.issuesDir())
	if err != nil {
		return fmt.Errorf("open issues dir for sync: %w", err)
	}
	defer dir.Close()

	if err := dir.Sync(); err != nil {
		return fmt.Errorf("fsync issues dir: %w", err)
	}

	return nil
}

func (s *Store) readAll() ([]*Issue, []error) {
	entries, err := os.ReadDir(s.issuesDir())
	if err != nil {
		return nil, nil
	}

	issues := make([]*Issue, 0, len(entries))
	var errs []error

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		id := strings.TrimSuffix(name, ".json")
		issue, err := s.readIssue(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		issues = append(issues, issue)
	}

	return issues, errs
}

func (s *Store) resolveID(partial string) (string, error) {
	entries, err := os.ReadDir(s.issuesDir())
	if err != nil {
		return "", fmt.Errorf("read issues dir: %w", err)
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}

	for _, id := range ids {
		if id == partial {
			return id, nil
		}
	}

	matches := map[string]struct{}{}
	for _, id := range ids {
		if strings.HasPrefix(id, partial) {
			matches[id] = struct{}{}
		}
		hexSuffix := id
		if idx := strings.LastIndex(id, "-"); idx >= 0 && idx+1 < len(id) {
			hexSuffix = id[idx+1:]
		}
		if strings.HasPrefix(hexSuffix, partial) {
			matches[id] = struct{}{}
		}
	}

	switch len(matches) {
	case 0:
		return "", ErrNotFound
	case 1:
		for id := range matches {
			return id, nil
		}
	}

	return "", ErrAmbiguousID
}

func (s *Store) withLock(fn func() error) error {
	lockPath := filepath.Join(s.tqDir, "lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}

	return fn()
}
