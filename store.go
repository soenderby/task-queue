package tq

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func (s *Store) nowRFC3339() string {
	return s.now().UTC().Format(time.RFC3339)
}

// Create creates a new issue.
func (s *Store) Create(opts CreateOpts) (*Issue, error) {
	if err := validateTitle(opts.Title); err != nil {
		return nil, err
	}

	priority := DefaultPriority
	if opts.Priority != nil {
		priority = *opts.Priority
	}
	if err := validatePriority(priority); err != nil {
		return nil, err
	}

	issueType := opts.Type
	if issueType == "" {
		issueType = DefaultType
	}

	var created *Issue
	err := s.withLock(func() error {
		for {
			id, err := generateID(s.prefix)
			if err != nil {
				return fmt.Errorf("generate id: %w", err)
			}
			if _, err := os.Stat(s.issuePath(id)); err == nil {
				continue // collision, try again
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("check id collision for %s: %w", id, err)
			}

			now := s.nowRFC3339()
			created = &Issue{
				ID:          id,
				Title:       opts.Title,
				Description: opts.Description,
				Status:      StatusOpen,
				Priority:    priority,
				Type:        issueType,
				Assignee:    opts.Assignee,
				Labels:      append([]string(nil), opts.Labels...),
				CreatedAt:   now,
				CreatedBy:   opts.CreatedBy,
				UpdatedAt:   now,
			}

			if err := s.writeIssue(created); err != nil {
				return err
			}
			return nil
		}
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

// Show returns one issue, resolving partial IDs.
func (s *Store) Show(id string) (*Issue, error) {
	resolvedID, err := s.resolveID(id)
	if err != nil {
		return nil, err
	}
	return s.readIssue(resolvedID)
}

// List returns filtered issues.
func (s *Store) List(filter ListFilter) ([]*Issue, error) {
	issues, _ := s.readAll()

	statusAllowed := map[string]struct{}{}
	for _, st := range filter.Status {
		statusAllowed[st] = struct{}{}
	}

	labelAllowed := filter.Label
	filtered := make([]*Issue, 0, len(issues))
	for _, issue := range issues {
		if len(filter.Status) == 0 {
			if issue.Status == StatusClosed {
				continue
			}
		} else {
			if _, ok := statusAllowed[issue.Status]; !ok {
				continue
			}
		}

		if filter.Assignee != "" && issue.Assignee != filter.Assignee {
			continue
		}
		if filter.Priority != nil && issue.Priority != *filter.Priority {
			continue
		}

		if len(labelAllowed) > 0 {
			allLabelsPresent := true
			for _, want := range labelAllowed {
				if !hasString(issue.Labels, want) {
					allLabelsPresent = false
					break
				}
			}
			if !allLabelsPresent {
				continue
			}
		}

		filtered = append(filtered, issue)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Priority != filtered[j].Priority {
			return filtered[i].Priority < filtered[j].Priority
		}
		if filtered[i].CreatedAt != filtered[j].CreatedAt {
			return filtered[i].CreatedAt < filtered[j].CreatedAt
		}
		return filtered[i].ID < filtered[j].ID
	})

	return filtered, nil
}

// Claim claims an issue for an actor.
func (s *Store) Claim(id string, actor string) (*Issue, error) {
	var claimed *Issue
	err := s.withLock(func() error {
		resolvedID, err := s.resolveID(id)
		if err != nil {
			return err
		}

		issue, err := s.readIssue(resolvedID)
		if err != nil {
			return err
		}

		if issue.Status != StatusOpen {
			return fmt.Errorf("cannot claim %s: %w (status: %s, assignee: %s)", issue.ID, ErrAlreadyClaimed, issue.Status, issue.Assignee)
		}

		var unresolved []string
		for _, blockerID := range issue.BlockedBy {
			blocker, err := s.readIssue(blockerID)
			if err != nil {
				if errors.Is(err, ErrNotFound) || errors.Is(err, ErrCorruptFile) {
					unresolved = append(unresolved, blockerID)
					continue
				}
				return err
			}
			if blocker.Status != StatusClosed {
				unresolved = append(unresolved, blockerID)
			}
		}

		if len(unresolved) > 0 {
			return fmt.Errorf("cannot claim %s: %w by %s", issue.ID, ErrBlocked, strings.Join(unresolved, ", "))
		}

		issue.Status = StatusInProgress
		issue.Assignee = actor
		issue.UpdatedAt = s.nowRFC3339()

		if err := s.writeIssue(issue); err != nil {
			return err
		}
		claimed = issue
		return nil
	})
	if err != nil {
		return nil, err
	}

	return claimed, nil
}

// Update updates mutable issue fields.
func (s *Store) Update(id string, opts UpdateOpts) (*Issue, error) {
	var updated *Issue
	err := s.withLock(func() error {
		resolvedID, err := s.resolveID(id)
		if err != nil {
			return err
		}

		issue, err := s.readIssue(resolvedID)
		if err != nil {
			return err
		}

		if opts.Status != nil {
			if issue.Status == StatusClosed {
				return ErrInvalidStatus
			}
			if err := validateUpdateStatusTransition(issue.Status, *opts.Status); err != nil {
				return err
			}
			issue.Status = *opts.Status
		}

		if opts.Priority != nil {
			if err := validatePriority(*opts.Priority); err != nil {
				return err
			}
			issue.Priority = *opts.Priority
		}

		if opts.Assignee != nil {
			issue.Assignee = *opts.Assignee
		}
		if opts.Description != nil {
			issue.Description = *opts.Description
		}

		if len(opts.AddLabels) > 0 {
			for _, add := range opts.AddLabels {
				if !hasString(issue.Labels, add) {
					issue.Labels = append(issue.Labels, add)
				}
			}
		}

		if len(opts.RemoveLabels) > 0 {
			for _, remove := range opts.RemoveLabels {
				issue.Labels = removeString(issue.Labels, remove)
			}
			if len(issue.Labels) == 0 {
				issue.Labels = nil
			}
		}

		issue.UpdatedAt = s.nowRFC3339()
		if err := s.writeIssue(issue); err != nil {
			return err
		}
		updated = issue
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Close closes an issue.
func (s *Store) Close(id string, reason string, actor string) (*Issue, error) {
	var closed *Issue
	err := s.withLock(func() error {
		resolvedID, err := s.resolveID(id)
		if err != nil {
			return err
		}

		issue, err := s.readIssue(resolvedID)
		if err != nil {
			return err
		}

		if issue.Status != StatusOpen && issue.Status != StatusInProgress {
			return ErrInvalidStatus
		}

		now := s.nowRFC3339()
		issue.Status = StatusClosed
		issue.ClosedAt = now
		issue.CloseReason = reason
		if actor != "" {
			issue.Assignee = actor
		}
		issue.UpdatedAt = now

		if err := s.writeIssue(issue); err != nil {
			return err
		}
		closed = issue
		return nil
	})
	if err != nil {
		return nil, err
	}
	return closed, nil
}

// Reopen reopens a closed issue.
func (s *Store) Reopen(id string) (*Issue, error) {
	var reopened *Issue
	err := s.withLock(func() error {
		resolvedID, err := s.resolveID(id)
		if err != nil {
			return err
		}

		issue, err := s.readIssue(resolvedID)
		if err != nil {
			return err
		}

		if issue.Status != StatusClosed {
			return ErrInvalidStatus
		}

		issue.Status = StatusOpen
		issue.ClosedAt = ""
		issue.CloseReason = ""
		issue.UpdatedAt = s.nowRFC3339()

		if err := s.writeIssue(issue); err != nil {
			return err
		}
		reopened = issue
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reopened, nil
}

// Comment appends a comment to an issue.
func (s *Store) Comment(id string, author string, text string) (*Issue, error) {
	var updated *Issue
	err := s.withLock(func() error {
		resolvedID, err := s.resolveID(id)
		if err != nil {
			return err
		}

		issue, err := s.readIssue(resolvedID)
		if err != nil {
			return err
		}

		commentAt := s.nowRFC3339()
		issue.Comments = append(issue.Comments, Comment{
			Author:    author,
			Text:      text,
			CreatedAt: commentAt,
		})
		issue.UpdatedAt = commentAt

		if err := s.writeIssue(issue); err != nil {
			return err
		}
		updated = issue
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func hasString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func removeString(values []string, remove string) []string {
	result := make([]string, 0, len(values))
	for _, v := range values {
		if v == remove {
			continue
		}
		result = append(result, v)
	}
	return result
}
