package tq

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func intPtr(v int) *int { return &v }

func strPtr(v string) *string { return &v }

func TestCreateShow(t *testing.T) {
	_, s := setupStore(t)

	created, err := s.Create(CreateOpts{
		Title:       "Fix login timeout",
		Description: "Investigate timeout value",
		Priority:    intPtr(1),
		Type:        "bug",
		Labels:      []string{"ck:auth", "px:exclusive"},
		Assignee:    "agent-1",
		CreatedBy:   "jsk",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if !strings.HasPrefix(created.ID, "orca-") {
		t.Fatalf("unexpected created ID: %q", created.ID)
	}
	if created.Status != StatusOpen {
		t.Fatalf("expected status open, got %q", created.Status)
	}
	if created.Priority != 1 {
		t.Fatalf("expected priority 1, got %d", created.Priority)
	}

	shown, err := s.Show(created.ID)
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if shown.Title != "Fix login timeout" {
		t.Fatalf("unexpected title: %q", shown.Title)
	}

	suffix := strings.TrimPrefix(created.ID, "orca-")
	partial := suffix[:3]
	shownByPartial, err := s.Show(partial)
	if err != nil {
		t.Fatalf("Show by partial ID failed: %v", err)
	}
	if shownByPartial.ID != created.ID {
		t.Fatalf("expected ID %q, got %q", created.ID, shownByPartial.ID)
	}
}

func TestCreateDefaults(t *testing.T) {
	_, s := setupStore(t)
	created, err := s.Create(CreateOpts{Title: "Default values test"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if created.Priority != DefaultPriority {
		t.Fatalf("expected default priority %d, got %d", DefaultPriority, created.Priority)
	}
	if created.Type != DefaultType {
		t.Fatalf("expected default type %q, got %q", DefaultType, created.Type)
	}
}

func TestCreateTitleRequired(t *testing.T) {
	_, s := setupStore(t)
	_, err := s.Create(CreateOpts{Title: ""})
	if !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("expected ErrTitleRequired, got: %v", err)
	}
}

func TestListDefaultAndFilters(t *testing.T) {
	_, s := setupStore(t)

	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, Labels: []string{"l1", "l2"}, Assignee: "x", CreatedAt: "2026-03-22T09:00:00Z", UpdatedAt: "2026-03-22T09:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-b1b2", Title: "B", Status: StatusInProgress, Priority: 1, Labels: []string{"l1"}, Assignee: "y", CreatedAt: "2026-03-22T08:00:00Z", UpdatedAt: "2026-03-22T08:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-c1b2", Title: "C", Status: StatusClosed, Priority: 0, Labels: []string{"l2"}, Assignee: "x", CreatedAt: "2026-03-22T07:00:00Z", UpdatedAt: "2026-03-22T07:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	all, err := s.List(ListFilter{})
	if err != nil {
		t.Fatalf("List default failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 non-closed issues, got %d", len(all))
	}
	if all[0].ID != "orca-b1b2" || all[1].ID != "orca-a1b2" {
		t.Fatalf("unexpected sort/order: %s, %s", all[0].ID, all[1].ID)
	}

	byStatus, err := s.List(ListFilter{Status: []string{StatusClosed}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byStatus) != 1 || byStatus[0].ID != "orca-c1b2" {
		t.Fatalf("status filter failed: %+v", byStatus)
	}

	byLabel, err := s.List(ListFilter{Label: []string{"l1", "l2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byLabel) != 1 || byLabel[0].ID != "orca-a1b2" {
		t.Fatalf("label filter failed: %+v", byLabel)
	}

	byAssignee, err := s.List(ListFilter{Assignee: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAssignee) != 1 || byAssignee[0].ID != "orca-a1b2" {
		t.Fatalf("assignee filter failed: %+v", byAssignee)
	}

	byPriority, err := s.List(ListFilter{Priority: intPtr(1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPriority) != 1 || byPriority[0].ID != "orca-b1b2" {
		t.Fatalf("priority filter failed: %+v", byPriority)
	}
}

func TestClaim(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{
		ID:        "orca-a1b2",
		Title:     "Claim me",
		Status:    StatusOpen,
		Priority:  2,
		CreatedAt: "2026-03-22T10:00:00Z",
		UpdatedAt: "2026-03-22T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.Claim("orca-a1b2", "agent-1")
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}
	if claimed.Status != StatusInProgress {
		t.Fatalf("expected in_progress, got %q", claimed.Status)
	}
	if claimed.Assignee != "agent-1" {
		t.Fatalf("expected assignee agent-1, got %q", claimed.Assignee)
	}
	if claimed.UpdatedAt != fixedNow().Format(time.RFC3339) {
		t.Fatalf("unexpected updated_at: %q", claimed.UpdatedAt)
	}
}

func TestClaimAlreadyClaimed(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Claimed", Status: StatusInProgress, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z", Assignee: "agent-x"}); err != nil {
		t.Fatal(err)
	}

	_, err := s.Claim("orca-a1b2", "agent-1")
	if !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("expected ErrAlreadyClaimed, got: %v", err)
	}
}

func TestClaimBlocked(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-block", Title: "Blocker", Status: StatusOpen, Priority: 2, CreatedAt: "2026-03-22T09:00:00Z", UpdatedAt: "2026-03-22T09:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-block"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	_, err := s.Claim("orca-a1b2", "agent-1")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got: %v", err)
	}
}

func TestClaimBlockedOrphanAndCorrupt(t *testing.T) {
	t.Run("orphan blocker", func(t *testing.T) {
		_, s := setupStore(t)
		if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-missing"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		_, err := s.Claim("orca-a1b2", "agent-1")
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("expected ErrBlocked, got: %v", err)
		}
	})

	t.Run("corrupt blocker", func(t *testing.T) {
		_, s := setupStore(t)
		if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-dead"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		badPath := filepath.Join(s.issuesDir(), "orca-dead.json")
		if err := os.WriteFile(badPath, []byte("{not json}"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := s.Claim("orca-a1b2", "agent-1")
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("expected ErrBlocked, got: %v", err)
		}
	})
}

func TestClaimUnblocked(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-block", Title: "Blocker", Status: StatusClosed, Priority: 2, CreatedAt: "2026-03-22T09:00:00Z", UpdatedAt: "2026-03-22T09:00:00Z", ClosedAt: "2026-03-22T09:30:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-block"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.Claim("orca-a1b2", "agent-1")
	if err != nil {
		t.Fatalf("claim should succeed, got: %v", err)
	}
	if claimed.Status != StatusInProgress {
		t.Fatalf("expected in_progress, got %q", claimed.Status)
	}
}

func TestUpdate(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusInProgress, Priority: 2, Description: "old", Assignee: "agent-1", Labels: []string{"l1"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	updated, err := s.Update("orca-a1b2", UpdateOpts{
		Status:       strPtr(StatusOpen),
		Priority:     intPtr(1),
		Assignee:     strPtr(""),
		Description:  strPtr(""),
		AddLabels:    []string{"l2", "l1"},
		RemoveLabels: []string{"l1", "missing"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.Status != StatusOpen {
		t.Fatalf("expected open status, got %q", updated.Status)
	}
	if updated.Priority != 1 {
		t.Fatalf("expected priority 1, got %d", updated.Priority)
	}
	if updated.Assignee != "" {
		t.Fatalf("expected assignee cleared, got %q", updated.Assignee)
	}
	if updated.Description != "" {
		t.Fatalf("expected description cleared, got %q", updated.Description)
	}
	if len(updated.Labels) != 1 || updated.Labels[0] != "l2" {
		t.Fatalf("unexpected labels after update: %+v", updated.Labels)
	}
}

func TestUpdateInvalidStatusTransitions(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusOpen, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	_, err := s.Update("orca-a1b2", UpdateOpts{Status: strPtr(StatusInProgress)})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus for in_progress, got: %v", err)
	}
	_, err = s.Update("orca-a1b2", UpdateOpts{Status: strPtr(StatusClosed)})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus for closed, got: %v", err)
	}
	_, err = s.Update("orca-a1b2", UpdateOpts{Status: strPtr(StatusOpen)})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus for open->open, got: %v", err)
	}
}

func TestUpdateOnClosedIssue(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusClosed, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z", ClosedAt: "2026-03-22T11:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	updated, err := s.Update("orca-a1b2", UpdateOpts{Priority: intPtr(1), Assignee: strPtr("agent-2"), Description: strPtr("desc"), AddLabels: []string{"l1"}})
	if err != nil {
		t.Fatalf("update closed mutable fields failed: %v", err)
	}
	if updated.Priority != 1 || updated.Assignee != "agent-2" || updated.Description != "desc" {
		t.Fatalf("unexpected updated closed issue: %+v", updated)
	}

	_, err = s.Update("orca-a1b2", UpdateOpts{Status: strPtr(StatusOpen)})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus for closed status update, got: %v", err)
	}
}

func TestUpdateOnCorruptTargetIssue(t *testing.T) {
	_, s := setupStore(t)
	badPath := filepath.Join(s.issuesDir(), "orca-a1b2.json")
	if err := os.WriteFile(badPath, []byte("{not json}"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := s.Update("orca-a1b2", UpdateOpts{Priority: intPtr(1)})
	if !errors.Is(err, ErrCorruptFile) {
		t.Fatalf("expected ErrCorruptFile, got: %v", err)
	}
}

func TestClose(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusInProgress, Priority: 2, Assignee: "agent-1", CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	closed, err := s.Close("orca-a1b2", "done", "agent-2")
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if closed.Status != StatusClosed {
		t.Fatalf("expected closed status, got %q", closed.Status)
	}
	if closed.CloseReason != "done" {
		t.Fatalf("expected close reason 'done', got %q", closed.CloseReason)
	}
	if closed.Assignee != "agent-2" {
		t.Fatalf("expected assignee agent-2, got %q", closed.Assignee)
	}
	if closed.ClosedAt == "" {
		t.Fatal("expected closed_at to be set")
	}

	_, err = s.Close("orca-a1b2", "again", "")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus closing already closed, got: %v", err)
	}
}

func TestReopen(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusClosed, Priority: 2, Assignee: "agent-1", CloseReason: "done", ClosedAt: "2026-03-22T11:00:00Z", CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T11:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := s.Reopen("orca-a1b2")
	if err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	if reopened.Status != StatusOpen {
		t.Fatalf("expected open status, got %q", reopened.Status)
	}
	if reopened.ClosedAt != "" || reopened.CloseReason != "" {
		t.Fatalf("expected close metadata cleared, got closed_at=%q reason=%q", reopened.ClosedAt, reopened.CloseReason)
	}
	if reopened.Assignee != "agent-1" {
		t.Fatalf("expected assignee preserved, got %q", reopened.Assignee)
	}

	_, err = s.Reopen("orca-a1b2")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus reopening non-closed, got: %v", err)
	}
}

func TestComment(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "Target", Status: StatusOpen, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	updated, err := s.Comment("orca-a1b2", "agent-1", "Investigated issue")
	if err != nil {
		t.Fatalf("Comment failed: %v", err)
	}
	if len(updated.Comments) != 1 {
		t.Fatalf("expected one comment, got %d", len(updated.Comments))
	}
	if updated.Comments[0].Author != "agent-1" || updated.Comments[0].Text != "Investigated issue" {
		t.Fatalf("unexpected comment: %+v", updated.Comments[0])
	}

	badPath := filepath.Join(s.issuesDir(), "orca-bad1.json")
	if err := os.WriteFile(badPath, []byte("{not json}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Comment("orca-bad1", "agent-1", "x")
	if !errors.Is(err, ErrCorruptFile) {
		t.Fatalf("expected ErrCorruptFile, got: %v", err)
	}
}

func TestDepAdd(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")
	writeTestIssue(t, s, "orca-b1b2")

	if err := s.DepAdd("orca-a1b2", "orca-b1b2"); err != nil {
		t.Fatalf("DepAdd failed: %v", err)
	}

	updated, err := s.readIssue("orca-a1b2")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != "orca-b1b2" {
		t.Fatalf("unexpected blocked_by: %+v", updated.BlockedBy)
	}
}

func TestDepAddErrors(t *testing.T) {
	t.Run("self dependency", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-a1b2")
		err := s.DepAdd("orca-a1b2", "orca-a1b2")
		if !errors.Is(err, ErrSelfDep) {
			t.Fatalf("expected ErrSelfDep, got: %v", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-a1b2")
		writeTestIssue(t, s, "orca-b1b2")
		if err := s.DepAdd("orca-a1b2", "orca-b1b2"); err != nil {
			t.Fatalf("first dep add failed: %v", err)
		}
		err := s.DepAdd("orca-a1b2", "orca-b1b2")
		if !errors.Is(err, ErrDupDep) {
			t.Fatalf("expected ErrDupDep, got: %v", err)
		}
	})

	t.Run("not found issue", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-b1b2")
		err := s.DepAdd("orca-missing", "orca-b1b2")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("not found blocker", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-a1b2")
		err := s.DepAdd("orca-a1b2", "orca-missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-a1b2")
		writeTestIssue(t, s, "orca-b1b2")
		writeTestIssue(t, s, "orca-c1b2")
		if err := s.DepAdd("orca-a1b2", "orca-b1b2"); err != nil {
			t.Fatal(err)
		}
		if err := s.DepAdd("orca-b1b2", "orca-c1b2"); err != nil {
			t.Fatal(err)
		}
		err := s.DepAdd("orca-c1b2", "orca-a1b2")
		if !errors.Is(err, ErrCycleDetected) {
			t.Fatalf("expected ErrCycleDetected, got: %v", err)
		}
	})
}

func TestDepAddClosedIssuesAllowed(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusClosed, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z", ClosedAt: "2026-03-22T11:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-b1b2", Title: "B", Status: StatusClosed, Priority: 2, CreatedAt: "2026-03-22T10:05:00Z", UpdatedAt: "2026-03-22T10:05:00Z", ClosedAt: "2026-03-22T11:05:00Z"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DepAdd("orca-a1b2", "orca-b1b2"); err != nil {
		t.Fatalf("DepAdd should allow closed issues, got: %v", err)
	}
}

func TestDepRemove(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-b1b2"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	writeTestIssue(t, s, "orca-b1b2")

	if err := s.DepRemove("orca-a1b2", "orca-b1b2"); err != nil {
		t.Fatalf("DepRemove failed: %v", err)
	}

	updated, err := s.readIssue("orca-a1b2")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected no blockers, got: %+v", updated.BlockedBy)
	}
}

func TestDepRemoveErrors(t *testing.T) {
	t.Run("non-existent edge", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-a1b2")
		writeTestIssue(t, s, "orca-b1b2")

		err := s.DepRemove("orca-a1b2", "orca-b1b2")
		if !errors.Is(err, ErrDepNotFound) {
			t.Fatalf("expected ErrDepNotFound, got: %v", err)
		}
	})

	t.Run("missing issue", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-b1b2")
		err := s.DepRemove("orca-missing", "orca-b1b2")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}
	})
}

func TestDepList(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-b1b2"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-b1b2", Title: "B", Status: StatusClosed, Priority: 2, CreatedAt: "2026-03-22T09:00:00Z", UpdatedAt: "2026-03-22T09:00:00Z", ClosedAt: "2026-03-22T09:30:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-c1b2", Title: "C", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-a1b2"}, CreatedAt: "2026-03-22T11:00:00Z", UpdatedAt: "2026-03-22T11:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	graph, err := s.DepList("orca-a1b2")
	if err != nil {
		t.Fatalf("DepList failed: %v", err)
	}
	if len(graph.BlockedBy) != 1 || graph.BlockedBy[0].ID != "orca-b1b2" {
		t.Fatalf("unexpected blocked_by list: %+v", graph.BlockedBy)
	}
	if len(graph.Blocks) != 1 || graph.Blocks[0].ID != "orca-c1b2" {
		t.Fatalf("unexpected blocks list: %+v", graph.Blocks)
	}
}

func TestReadyEndToEnd(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-b1b2", Title: "B", Status: StatusOpen, Priority: 1, BlockedBy: []string{"orca-c1b2"}, CreatedAt: "2026-03-22T09:00:00Z", UpdatedAt: "2026-03-22T09:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-c1b2", Title: "C", Status: StatusClosed, Priority: 0, CreatedAt: "2026-03-22T08:00:00Z", UpdatedAt: "2026-03-22T08:00:00Z", ClosedAt: "2026-03-22T08:30:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-d1b2", Title: "D", Status: StatusInProgress, Priority: 0, CreatedAt: "2026-03-22T07:00:00Z", UpdatedAt: "2026-03-22T07:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	ready, err := s.Ready()
	if err != nil {
		t.Fatalf("Ready failed: %v", err)
	}
	if len(ready) != 2 {
		t.Fatalf("expected 2 ready issues, got %d", len(ready))
	}
	if ready[0].ID != "orca-b1b2" || ready[1].ID != "orca-a1b2" {
		t.Fatalf("unexpected ready order: %s, %s", ready[0].ID, ready[1].ID)
	}
}

func TestReadyOrphanAndCorruptCases(t *testing.T) {
	t.Run("orphan blocker excluded", func(t *testing.T) {
		_, s := setupStore(t)
		if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 1, BlockedBy: []string{"orca-missing"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		ready, err := s.Ready()
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) != 0 {
			t.Fatalf("expected no ready issues, got %+v", ready)
		}
	})

	t.Run("malformed issue file skipped", func(t *testing.T) {
		_, s := setupStore(t)
		writeTestIssue(t, s, "orca-a1b2")
		if err := os.WriteFile(filepath.Join(s.issuesDir(), "orca-bad1.json"), []byte("{not json}"), 0o644); err != nil {
			t.Fatal(err)
		}

		ready, err := s.Ready()
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) != 1 || ready[0].ID != "orca-a1b2" {
			t.Fatalf("unexpected ready result with malformed file: %+v", ready)
		}
	})

	t.Run("malformed blocker excluded", func(t *testing.T) {
		_, s := setupStore(t)
		if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 1, BlockedBy: []string{"orca-b1b2"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.issuesDir(), "orca-b1b2.json"), []byte("{not json}"), 0o644); err != nil {
			t.Fatal(err)
		}

		ready, err := s.Ready()
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) != 0 {
			t.Fatalf("expected blocked issue to be excluded, got %+v", ready)
		}
	})
}

func findCheck(t *testing.T, report *DoctorReport, id string) DoctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("check %q not found", id)
	return DoctorCheck{}
}

func TestDoctorClean(t *testing.T) {
	_, s := setupStore(t)
	writeTestIssue(t, s, "orca-a1b2")

	report, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor failed: %v", err)
	}
	if !report.OK {
		t.Fatalf("expected report.OK=true, got false: %+v", report)
	}

	ids := []string{
		"workspace_dir",
		"config_valid",
		"issues_dir",
		"issue_json_valid",
		"issue_filename_matches_id",
		"orphan_dependencies",
		"dependency_cycles",
		"stale_temp_files",
	}
	for _, id := range ids {
		check := findCheck(t, report, id)
		if check.Status != "pass" {
			t.Fatalf("expected check %q to pass, got %q (%s)", id, check.Status, check.Message)
		}
	}
}

func TestDoctorOrphanDep(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-missing"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, "orphan_dependencies")
	if check.Status != "fail" {
		t.Fatalf("expected orphan_dependencies fail, got %q", check.Status)
	}
	if report.OK {
		t.Fatal("expected report.OK=false when fail checks exist")
	}
}

func TestDoctorCycle(t *testing.T) {
	_, s := setupStore(t)
	if err := s.writeIssue(&Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-b1b2"}, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeIssue(&Issue{ID: "orca-b1b2", Title: "B", Status: StatusOpen, Priority: 2, BlockedBy: []string{"orca-a1b2"}, CreatedAt: "2026-03-22T10:05:00Z", UpdatedAt: "2026-03-22T10:05:00Z"}); err != nil {
		t.Fatal(err)
	}

	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, "dependency_cycles")
	if check.Status != "fail" {
		t.Fatalf("expected dependency_cycles fail, got %q", check.Status)
	}
}

func TestDoctorCorruptJSON(t *testing.T) {
	_, s := setupStore(t)
	if err := os.WriteFile(filepath.Join(s.issuesDir(), "orca-a1b2.json"), []byte("{not json}"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, "issue_json_valid")
	if check.Status != "fail" {
		t.Fatalf("expected issue_json_valid fail, got %q", check.Status)
	}
}

func TestDoctorFilenameMismatch(t *testing.T) {
	_, s := setupStore(t)
	issue := &Issue{
		ID:        "orca-aaaa",
		Title:     "Mismatch",
		Status:    StatusOpen,
		Priority:  2,
		CreatedAt: "2026-03-22T10:00:00Z",
		UpdatedAt: "2026-03-22T10:00:00Z",
	}
	data, err := json.MarshalIndent(issue, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(s.issuesDir(), "orca-bbbb.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, "issue_filename_matches_id")
	if check.Status != "fail" {
		t.Fatalf("expected issue_filename_matches_id fail, got %q", check.Status)
	}
}

func TestDoctorStaleTmpWarnOnly(t *testing.T) {
	_, s := setupStore(t)
	if err := os.WriteFile(filepath.Join(s.issuesDir(), "orca-a1b2.json.tmp"), []byte("tmp"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := s.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, "stale_temp_files")
	if check.Status != "warn" {
		t.Fatalf("expected stale_temp_files warn, got %q", check.Status)
	}
	if !report.OK {
		t.Fatal("expected report.OK=true when only warnings exist")
	}
}
