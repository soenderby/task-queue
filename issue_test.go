package tq

import (
	"errors"
	"regexp"
	"testing"
)

func TestGenerateID(t *testing.T) {
	idPattern := regexp.MustCompile(`^orca-[0-9a-f]{4}$`)

	for i := 0; i < 20; i++ {
		id, err := generateID("orca")
		if err != nil {
			t.Fatalf("generateID returned error: %v", err)
		}
		if !idPattern.MatchString(id) {
			t.Fatalf("generated id does not match expected pattern: %q", id)
		}
	}
}

func TestValidatePrefix(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		wantError bool
	}{
		{name: "valid simple", prefix: "orca", wantError: false},
		{name: "valid hyphen", prefix: "my-project", wantError: false},
		{name: "valid min len", prefix: "ab", wantError: false},
		{name: "invalid empty", prefix: "", wantError: true},
		{name: "invalid uppercase", prefix: "A", wantError: true},
		{name: "invalid too short", prefix: "a", wantError: true},
		{name: "invalid starts hyphen", prefix: "-abc", wantError: true},
		{name: "invalid space", prefix: "has space", wantError: true},
		{name: "invalid too long", prefix: "abcdefghijklmnopqrstuvwxyzabcdefg", wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePrefix(tc.prefix)
			if tc.wantError {
				if !errors.Is(err, ErrInvalidPrefix) {
					t.Fatalf("expected ErrInvalidPrefix, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

func TestValidatePriority(t *testing.T) {
	valid := []int{0, 1, 2, 3, 4}
	for _, p := range valid {
		if err := validatePriority(p); err != nil {
			t.Fatalf("priority %d should be valid, got err: %v", p, err)
		}
	}

	invalid := []int{-1, 5, 100}
	for _, p := range invalid {
		err := validatePriority(p)
		if !errors.Is(err, ErrInvalidPriority) {
			t.Fatalf("priority %d should return ErrInvalidPriority, got: %v", p, err)
		}
	}
}

func TestValidateStatus(t *testing.T) {
	valid := []string{StatusOpen, StatusInProgress, StatusClosed}
	for _, s := range valid {
		if err := validateStatus(s); err != nil {
			t.Fatalf("status %q should be valid, got err: %v", s, err)
		}
	}

	invalid := []string{"", "OPEN", "blocked"}
	for _, s := range invalid {
		err := validateStatus(s)
		if !errors.Is(err, ErrInvalidStatus) {
			t.Fatalf("status %q should return ErrInvalidStatus, got: %v", s, err)
		}
	}
}

func TestValidateTitle(t *testing.T) {
	if err := validateTitle("Fix login timeout"); err != nil {
		t.Fatalf("expected valid title, got err: %v", err)
	}
	if err := validateTitle(""); !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("expected ErrTitleRequired, got: %v", err)
	}
}

func TestValidateUpdateStatusTransition(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		next      string
		wantError bool
	}{
		{name: "in_progress to open allowed", current: StatusInProgress, next: StatusOpen, wantError: false},
		{name: "open to open not allowed", current: StatusOpen, next: StatusOpen, wantError: true},
		{name: "closed to open not allowed", current: StatusClosed, next: StatusOpen, wantError: true},
		{name: "open to in_progress not allowed", current: StatusOpen, next: StatusInProgress, wantError: true},
		{name: "open to closed not allowed", current: StatusOpen, next: StatusClosed, wantError: true},
		{name: "open to invalid status not allowed", current: StatusOpen, next: "bogus", wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUpdateStatusTransition(tc.current, tc.next)
			if tc.wantError {
				if !errors.Is(err, ErrInvalidStatus) {
					t.Fatalf("expected ErrInvalidStatus, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}
