package tq

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)
}

func setupStore(t *testing.T) (string, *Store) {
	t.Helper()
	root := t.TempDir()
	if err := Init(root, "orca"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return root, newStoreForTest(filepath.Join(root, ".tq"), fixedNow)
}

func writeTestIssue(t *testing.T, s *Store, id string) *Issue {
	t.Helper()
	issue := &Issue{
		ID:        id,
		Title:     "Test issue",
		Status:    StatusOpen,
		Priority:  2,
		CreatedAt: "2026-03-23T10:00:00Z",
		UpdatedAt: "2026-03-23T10:00:00Z",
	}
	if err := s.writeIssue(issue); err != nil {
		t.Fatalf("writeIssue(%s) failed: %v", id, err)
	}
	return issue
}

func TestInit(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, "orca"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tqDir := filepath.Join(root, ".tq")
	if st, err := os.Stat(tqDir); err != nil || !st.IsDir() {
		t.Fatalf("expected .tq dir to exist, stat err=%v", err)
	}

	issuesDir := filepath.Join(tqDir, "issues")
	if st, err := os.Stat(issuesDir); err != nil || !st.IsDir() {
		t.Fatalf("expected .tq/issues dir to exist, stat err=%v", err)
	}

	cfgPath := filepath.Join(tqDir, "config.json")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed reading config: %v", err)
	}
	var cfg workspaceConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("failed parsing config: %v", err)
	}
	if cfg.Version != 1 || cfg.IDPrefix != "orca" {
		t.Fatalf("unexpected config contents: %+v", cfg)
	}

	gitignoreData, err := os.ReadFile(filepath.Join(tqDir, ".gitignore"))
	if err != nil {
		t.Fatalf("failed reading .gitignore: %v", err)
	}
	if string(gitignoreData) != "lock\nissues/*.tmp\n" {
		t.Fatalf("unexpected .gitignore content: %q", string(gitignoreData))
	}
}

func TestInitDuplicate(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, "orca"); err != nil {
		t.Fatalf("first Init failed: %v", err)
	}
	if err := Init(root, "orca"); !errors.Is(err, ErrAlreadyInit) {
		t.Fatalf("expected ErrAlreadyInit, got: %v", err)
	}
}

func TestInitInvalidPrefix(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, "A"); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("expected ErrInvalidPrefix, got: %v", err)
	}
}

func TestOpen(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, "orca"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	if s.prefix != "orca" {
		t.Fatalf("expected prefix 'orca', got %q", s.prefix)
	}
	if s.tqDir != filepath.Join(root, ".tq") {
		t.Fatalf("unexpected tqDir: %q", s.tqDir)
	}
}

func TestOpenNotFound(t *testing.T) {
	root := t.TempDir()
	_, err := Open(root)
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got: %v", err)
	}
}

func TestReadWriteRoundTrip(t *testing.T) {
	_, s := setupStore(t)

	issue := &Issue{
		ID:          "orca-a1b2",
		Title:       "Fix login timeout",
		Description: "Investigate timeout value",
		Status:      StatusOpen,
		Priority:    1,
		Type:        "bug",
		Assignee:    "agent-1",
		Labels:      []string{"ck:auth"},
		BlockedBy:   []string{"orca-c3d4"},
		Comments: []Comment{{
			Author:    "agent-1",
			Text:      "started",
			CreatedAt: "2026-03-23T10:10:00Z",
		}},
		CreatedAt: "2026-03-23T10:00:00Z",
		CreatedBy: "jsk",
		UpdatedAt: "2026-03-23T10:10:00Z",
	}

	if err := s.writeIssue(issue); err != nil {
		t.Fatalf("writeIssue failed: %v", err)
	}

	got, err := s.readIssue("orca-a1b2")
	if err != nil {
		t.Fatalf("readIssue failed: %v", err)
	}

	if got.ID != issue.ID ||
		got.Title != issue.Title ||
		got.Description != issue.Description ||
		got.Status != issue.Status ||
		got.Priority != issue.Priority ||
		got.Type != issue.Type ||
		got.Assignee != issue.Assignee ||
		got.CreatedAt != issue.CreatedAt ||
		got.UpdatedAt != issue.UpdatedAt {
		t.Fatalf("issue mismatch, got=%+v want=%+v", got, issue)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "ck:auth" {
		t.Fatalf("unexpected labels: %+v", got.Labels)
	}
	if len(got.BlockedBy) != 1 || got.BlockedBy[0] != "orca-c3d4" {
		t.Fatalf("unexpected blocked_by: %+v", got.BlockedBy)
	}
	if len(got.Comments) != 1 || got.Comments[0].Text != "started" {
		t.Fatalf("unexpected comments: %+v", got.Comments)
	}
}

func TestReadAllEmpty(t *testing.T) {
	_, s := setupStore(t)

	issues, errs := s.readAll()
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d", len(issues))
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d", len(errs))
	}
}

func TestReadAllSkipsTemp(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")

	tmpPath := filepath.Join(s.issuesDir(), "orca-dead.json.tmp")
	if err := os.WriteFile(tmpPath, []byte("tmp"), 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	issues, errs := s.readAll()
	if len(errs) != 0 {
		t.Fatalf("expected no read errors, got %d", len(errs))
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].ID != "orca-a1b2" {
		t.Fatalf("unexpected issue id: %q", issues[0].ID)
	}
}

func TestReadAllSkipsCorrupt(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")

	badPath := filepath.Join(s.issuesDir(), "orca-bad1.json")
	if err := os.WriteFile(badPath, []byte("{this is not json}"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	issues, errs := s.readAll()
	if len(issues) != 1 {
		t.Fatalf("expected 1 valid issue, got %d", len(issues))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !errors.Is(errs[0], ErrCorruptFile) {
		t.Fatalf("expected ErrCorruptFile, got: %v", errs[0])
	}
}

func TestResolveIDExact(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")
	writeTestIssue(t, s, "orca-c3d4")

	id, err := s.resolveID("orca-a1b2")
	if err != nil {
		t.Fatalf("resolveID failed: %v", err)
	}
	if id != "orca-a1b2" {
		t.Fatalf("expected orca-a1b2, got %q", id)
	}
}

func TestResolveIDPrefix(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")
	writeTestIssue(t, s, "orca-c3d4")

	id, err := s.resolveID("orca-a1b")
	if err != nil {
		t.Fatalf("resolveID failed: %v", err)
	}
	if id != "orca-a1b2" {
		t.Fatalf("expected orca-a1b2, got %q", id)
	}
}

func TestResolveIDHexSuffix(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")
	writeTestIssue(t, s, "orca-c3d4")

	id, err := s.resolveID("a1b")
	if err != nil {
		t.Fatalf("resolveID failed: %v", err)
	}
	if id != "orca-a1b2" {
		t.Fatalf("expected orca-a1b2, got %q", id)
	}
}

func TestResolveIDNotFound(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")

	_, err := s.resolveID("zzzz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestResolveIDAmbiguous(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")
	writeTestIssue(t, s, "orca-a1b9")

	_, err := s.resolveID("a1b")
	if !errors.Is(err, ErrAmbiguousID) {
		t.Fatalf("expected ErrAmbiguousID, got: %v", err)
	}
}
