package tq

import "sort"

// computeReady returns ready issues from a full issue set.
// Ready means status=open and all blockers exist and are closed.
func computeReady(all []*Issue) []*Issue {
	byID := make(map[string]*Issue, len(all))
	for _, issue := range all {
		byID[issue.ID] = issue
	}

	ready := make([]*Issue, 0, len(all))
	for _, issue := range all {
		if issue.Status != StatusOpen {
			continue
		}

		isReady := true
		for _, blockerID := range issue.BlockedBy {
			blocker, ok := byID[blockerID]
			if !ok || blocker.Status != StatusClosed {
				isReady = false
				break
			}
		}
		if isReady {
			ready = append(ready, issue)
		}
	}

	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority < ready[j].Priority
		}
		if ready[i].CreatedAt != ready[j].CreatedAt {
			return ready[i].CreatedAt < ready[j].CreatedAt
		}
		return ready[i].ID < ready[j].ID
	})

	return ready
}

// hasCycle returns true if adding edge from->to would create a dependency cycle.
// Edges are issue -> blocked_by issue.
func hasCycle(all []*Issue, from, to string) bool {
	if from == to {
		return true
	}

	adj := make(map[string][]string, len(all))
	for _, issue := range all {
		adj[issue.ID] = issue.BlockedBy
	}

	visited := map[string]struct{}{}
	stack := []string{to}

	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if n == from {
			return true
		}
		if _, ok := visited[n]; ok {
			continue
		}
		visited[n] = struct{}{}

		for _, next := range adj[n] {
			if _, seen := visited[next]; !seen {
				stack = append(stack, next)
			}
		}
	}

	return false
}
