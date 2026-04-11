package tq

import "testing"

func readyIssue(id, status string, priority int, createdAt string, blockedBy ...string) *Issue {
	return &Issue{
		ID:        id,
		Title:     id,
		Status:    status,
		Priority:  priority,
		BlockedBy: blockedBy,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func TestComputeReady(t *testing.T) {
	t.Run("no issues", func(t *testing.T) {
		got := computeReady(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty, got %d", len(got))
		}
	})

	t.Run("all open no deps sorted", func(t *testing.T) {
		all := []*Issue{
			readyIssue("orca-b", StatusOpen, 2, "2026-03-23T11:00:00Z"),
			readyIssue("orca-a", StatusOpen, 1, "2026-03-23T12:00:00Z"),
			readyIssue("orca-c", StatusOpen, 1, "2026-03-23T10:00:00Z"),
		}
		got := computeReady(all)
		if len(got) != 3 {
			t.Fatalf("expected 3, got %d", len(got))
		}
		if got[0].ID != "orca-c" || got[1].ID != "orca-a" || got[2].ID != "orca-b" {
			t.Fatalf("unexpected order: %s, %s, %s", got[0].ID, got[1].ID, got[2].ID)
		}
	})

	t.Run("blocked by open issue is not ready", func(t *testing.T) {
		all := []*Issue{
			readyIssue("orca-a", StatusOpen, 2, "2026-03-23T10:00:00Z", "orca-b"),
			readyIssue("orca-b", StatusOpen, 2, "2026-03-23T09:00:00Z"),
		}
		got := computeReady(all)
		if len(got) != 1 || got[0].ID != "orca-b" {
			t.Fatalf("expected only blocker issue ready, got %+v", got)
		}
	})

	t.Run("blocked by closed issue is ready", func(t *testing.T) {
		all := []*Issue{
			readyIssue("orca-a", StatusOpen, 2, "2026-03-23T10:00:00Z", "orca-b"),
			readyIssue("orca-b", StatusClosed, 2, "2026-03-23T09:00:00Z"),
		}
		got := computeReady(all)
		if len(got) != 1 || got[0].ID != "orca-a" {
			t.Fatalf("expected only orca-a ready, got %+v", got)
		}
	})

	t.Run("multiple blockers one open not ready", func(t *testing.T) {
		all := []*Issue{
			readyIssue("orca-a", StatusOpen, 2, "2026-03-23T10:00:00Z", "orca-b", "orca-c"),
			readyIssue("orca-b", StatusClosed, 2, "2026-03-23T09:00:00Z"),
			readyIssue("orca-c", StatusOpen, 2, "2026-03-23T08:00:00Z"),
		}
		got := computeReady(all)
		if len(got) != 1 || got[0].ID != "orca-c" {
			t.Fatalf("expected only orca-c ready, got %+v", got)
		}
	})

	t.Run("in_progress and closed excluded", func(t *testing.T) {
		all := []*Issue{
			readyIssue("orca-a", StatusInProgress, 1, "2026-03-23T10:00:00Z"),
			readyIssue("orca-b", StatusClosed, 1, "2026-03-23T10:00:00Z"),
			readyIssue("orca-c", StatusOpen, 1, "2026-03-23T10:00:00Z"),
		}
		got := computeReady(all)
		if len(got) != 1 || got[0].ID != "orca-c" {
			t.Fatalf("expected only open issue ready, got %+v", got)
		}
	})

	t.Run("orphan blocker is not ready", func(t *testing.T) {
		all := []*Issue{
			readyIssue("orca-a", StatusOpen, 1, "2026-03-23T10:00:00Z", "orca-missing"),
		}
		got := computeReady(all)
		if len(got) != 0 {
			t.Fatalf("expected empty ready list, got %+v", got)
		}
	})
}

func TestHasCycle(t *testing.T) {
	all := []*Issue{
		readyIssue("orca-a", StatusOpen, 1, "2026-03-23T10:00:00Z", "orca-b"),
		readyIssue("orca-b", StatusOpen, 1, "2026-03-23T10:00:00Z", "orca-c"),
		readyIssue("orca-c", StatusClosed, 1, "2026-03-23T10:00:00Z"),
	}

	if hasCycle(all, "orca-c", "orca-a") == false {
		t.Fatal("expected cycle for c -> a")
	}
	if hasCycle(all, "orca-a", "orca-c") {
		t.Fatal("did not expect cycle for a -> c")
	}
	if hasCycle(all, "orca-a", "orca-a") == false {
		t.Fatal("expected self-edge cycle")
	}

	// cycle through closed issues is still a cycle
	all2 := []*Issue{
		readyIssue("orca-x", StatusOpen, 1, "2026-03-23T10:00:00Z", "orca-y"),
		readyIssue("orca-y", StatusClosed, 1, "2026-03-23T10:00:00Z", "orca-z"),
		readyIssue("orca-z", StatusOpen, 1, "2026-03-23T10:00:00Z"),
	}
	if !hasCycle(all2, "orca-z", "orca-x") {
		t.Fatal("expected cycle through closed issue")
	}
}
