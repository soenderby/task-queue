package tq

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
)

// Issue represents a task queue issue persisted as JSON.
type Issue struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	Priority    int       `json:"priority"`
	Type        string    `json:"type,omitempty"`
	Assignee    string    `json:"assignee,omitempty"`
	Labels      []string  `json:"labels,omitempty"`
	BlockedBy   []string  `json:"blocked_by,omitempty"`
	Comments    []Comment `json:"comments,omitempty"`
	CreatedAt   string    `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
	UpdatedAt   string    `json:"updated_at"`
	ClosedAt    string    `json:"closed_at,omitempty"`
	CloseReason string    `json:"close_reason,omitempty"`
}

// Comment is an append-only issue comment.
type Comment struct {
	Author    string `json:"author"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// CreateOpts controls issue creation.
type CreateOpts struct {
	Title       string
	Description string
	Priority    *int // nil = default 2; 0-4 otherwise
	Type        string
	Labels      []string
	Assignee    string
	CreatedBy   string
}

// UpdateOpts controls mutable fields on an issue.
type UpdateOpts struct {
	Status       *string
	Priority     *int
	Assignee     *string
	Description  *string
	AddLabels    []string
	RemoveLabels []string
}

// ListFilter controls issue listing filters.
type ListFilter struct {
	Status   []string // empty = all non-closed
	Label    []string // all must match
	Assignee string
	Priority *int
}

// DepGraph is a dependency view for one issue.
type DepGraph struct {
	BlockedBy []*Issue `json:"blocked_by"`
	Blocks    []*Issue `json:"blocks"`
}

// DoctorReport is the result of running health checks.
type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
	OK     bool          `json:"ok"`
}

// DoctorCheck describes one health check result.
type DoctorCheck struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

var (
	ErrNotFound        = errors.New("issue not found")
	ErrAmbiguousID     = errors.New("ambiguous issue ID")
	ErrAlreadyClaimed  = errors.New("issue is not open")
	ErrBlocked         = errors.New("issue is blocked")
	ErrCycleDetected   = errors.New("dependency would create cycle")
	ErrSelfDep         = errors.New("cannot depend on self")
	ErrDupDep          = errors.New("dependency already exists")
	ErrDepNotFound     = errors.New("dependency does not exist")
	ErrNotInitialized  = errors.New("tq workspace not found")
	ErrAlreadyInit     = errors.New("tq workspace already exists")
	ErrInvalidStatus   = errors.New("invalid status value")
	ErrInvalidPriority = errors.New("priority must be 0-4")
	ErrInvalidPrefix   = errors.New("id prefix is invalid")
	ErrTitleRequired   = errors.New("title is required")
	ErrCorruptFile     = errors.New("issue file is corrupt")
)

const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusClosed     = "closed"

	DefaultPriority = 2
	DefaultType     = "task"
)

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// generateID returns an ID of the form "{prefix}-{4hex}".
func generateID(prefix string) (string, error) {
	buf := make([]byte, 2)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func validatePrefix(prefix string) error {
	if !prefixPattern.MatchString(prefix) {
		return ErrInvalidPrefix
	}
	return nil
}

func validatePriority(priority int) error {
	if priority < 0 || priority > 4 {
		return ErrInvalidPriority
	}
	return nil
}

func validateStatus(status string) error {
	switch status {
	case StatusOpen, StatusInProgress, StatusClosed:
		return nil
	default:
		return ErrInvalidStatus
	}
}

func validateTitle(title string) error {
	if title == "" {
		return ErrTitleRequired
	}
	return nil
}

func validateUpdateStatusTransition(current, next string) error {
	switch next {
	case StatusInProgress, StatusClosed:
		return ErrInvalidStatus
	case StatusOpen:
		if current != StatusInProgress {
			return ErrInvalidStatus
		}
		return nil
	default:
		return ErrInvalidStatus
	}
}
