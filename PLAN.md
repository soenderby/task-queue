# Implementation Plan

This plan translates DESIGN.md into a concrete, step-by-step build sequence. Each step ends with a passing `go test ./...` gate. Read DESIGN.md first — this plan does not repeat the design; it fills in the specifics the design leaves to the implementer.

---

## Design Clarifications

Three minor gaps from the design review are resolved here before any code is written:

**1. `update --status open` on an already-`open` issue.**
The transition table says `in_progress → open` is the allowed path. `update --status open` on an already-`open` issue fails with `ErrInvalidStatus`. An `open → open` transition is not listed as allowed and has no defined side effects.

**2. `--actor` on `tq claim` is required.**
The CLI synopsis shows it without brackets: `tq claim ID --actor NAME`. If omitted, print a usage error to stderr and exit 1. No sentinel error; this is a CLI input error, not a store error.

**3. Go version.**
Use Go 1.22. This is the minimum that includes `slices` (used for sorting) and `maps` (if needed) in the standard library, and has stable `syscall.Flock` on all target platforms (Linux, macOS, WSL).

---

## Step 0: Module and Directory Scaffold

**Goal:** A compilable Go module with the correct layout. No logic yet.

### Files to create

```
go.mod
cmd/tq/main.go          ← stub only
issue.go                ← stub only
store.go                ← stub only
ready.go                ← stub only
```

**`go.mod`:**
```
module github.com/soenderby/task-queue

go 1.22
```

No external dependencies. The module has zero third-party imports.

**`cmd/tq/main.go`** (stub):
```go
package main

func main() {}
```

**`issue.go`**, **`store.go`**, **`ready.go`** (stubs):
```go
package tq
```

**Verification:** `go build ./...` passes.

---

## Step 1: Types, Errors, and Validation

**Files:** `issue.go`, `issue_test.go`

**Goal:** All types, all sentinel errors, ID generation, and validation logic. No file I/O. All tests pass.

### 1a. Types

Declare in `issue.go` in the field order from the design (which also controls JSON serialization order via Go struct field ordering):

```go
type Issue struct { ... }    // as in DESIGN.md §Data Model
type Comment struct { ... }  // as in DESIGN.md §Data Model

type CreateOpts struct {
    Title       string
    Description string
    Priority    *int     // nil = default 2
    Type        string   // default "task" if empty
    Labels      []string
    Assignee    string
    CreatedBy   string
}

type UpdateOpts struct {
    Status       *string
    Priority     *int
    Assignee     *string  // non-nil empty string clears
    Description  *string  // non-nil empty string clears
    AddLabels    []string
    RemoveLabels []string
}

type ListFilter struct {
    Status   []string // empty = all non-closed
    Label    []string // all must match
    Assignee string
    Priority *int
}

type DepGraph struct {
    BlockedBy []*Issue `json:"blocked_by"`
    Blocks    []*Issue `json:"blocks"`
}

type DoctorReport struct {
    Checks []DoctorCheck `json:"checks"`
    OK     bool          `json:"ok"`
}

type DoctorCheck struct {
    ID      string `json:"id"`
    Name    string `json:"name"`
    Status  string `json:"status"`  // "pass", "warn", "fail"
    Message string `json:"message,omitempty"`
}
```

### 1b. Sentinel errors

Declare all 15 errors from DESIGN.md §Errors as package-level `var` with `errors.New`. Order them as listed in the design.

### 1c. Constants

```go
const (
    StatusOpen       = "open"
    StatusInProgress = "in_progress"
    StatusClosed     = "closed"

    DefaultPriority = 2
    DefaultType     = "task"
)
```

### 1d. ID generation

```go
// generateID returns a new ID of the form "{prefix}-{4 random hex chars}".
// Uses crypto/rand. Caller retries on collision.
func generateID(prefix string) (string, error)
```

Implementation: read 2 bytes from `crypto/rand.Read`, format as 4 lowercase hex characters with `fmt.Sprintf("%04x", ...)`, return `prefix + "-" + hex`.

### 1e. Validation functions

These are pure functions called by the store before writing. None perform I/O.

```go
// validatePrefix checks that a prefix matches ^[a-z][a-z0-9-]{1,31}$.
func validatePrefix(prefix string) error

// validatePriority checks that p is in [0,4].
func validatePriority(p int) error

// validateStatus checks that s is one of the three valid status strings.
func validateStatus(s string) error

// validateUpdateStatusTransition validates the status value in UpdateOpts:
//   - "in_progress" → always ErrInvalidStatus (use Claim)
//   - "closed"      → always ErrInvalidStatus (use Close)
//   - "open"        → only valid if current is StatusInProgress; otherwise ErrInvalidStatus
//   - anything else → ErrInvalidStatus
func validateUpdateStatusTransition(current, next string) error
```

### 1f. `issue_test.go`

Table-driven tests with `t.Run`:

| Test | Cases |
|---|---|
| `TestGenerateID` | Format matches `^[a-z][a-z0-9-]+-[0-9a-f]{4}$`; prefix is preserved; two calls produce different IDs (probabilistic) |
| `TestValidatePrefix` | Valid: `"orca"`, `"my-project"`, `"ab"` (2 chars). Invalid: `""`, `"A"` (uppercase), `"a"` (1 char), 33-char string, `"-start"`, `"has space"` |
| `TestValidatePriority` | Valid: 0,1,2,3,4. Invalid: -1, 5, 100 |
| `TestValidateStatus` | Valid: `"open"`, `"in_progress"`, `"closed"`. Invalid: `""`, `"OPEN"`, `"blocked"` |
| `TestValidateUpdateStatusTransition` | `(in_progress, open)` → nil; `(open, open)` → ErrInvalidStatus; `(closed, open)` → ErrInvalidStatus; `(open, in_progress)` → ErrInvalidStatus; `(open, closed)` → ErrInvalidStatus; `(open, "bogus")` → ErrInvalidStatus |

**Verification:** `go test ./... -run TestGenerate\|TestValidate`

---

## Step 2: Store — Init, Read, Write

**Files:** `store.go`, `store_test.go`

**Goal:** Store struct, initialization, file I/O primitives, partial ID resolution, and file locking. No business logic yet.

### 2a. Store struct

```go
type Store struct {
    tqDir string           // absolute path to .tq/
    now   func() time.Time // injectable clock; defaults to time.Now
}
```

The `tqDir` field is the `.tq/` directory itself (e.g. `/path/to/project/.tq`). All internal paths are built with `filepath.Join(s.tqDir, ...)`.

### 2b. `Init(dir, prefix string) error`

Package-level function (not a method). Steps:
1. Validate `prefix` with `validatePrefix`.
2. Build `tqDir = filepath.Join(dir, ".tq")`.
3. Check if `tqDir` already exists → `ErrAlreadyInit`.
4. `os.MkdirAll(filepath.Join(tqDir, "issues"), 0o755)`.
5. Write `config.json`: `{"version":1,"id_prefix":"<prefix>"}`.
6. Write `.gitignore` with content exactly:
   ```
   lock
   issues/*.tmp
   ```
   (trailing newline required for POSIX text file compliance).

### 2c. `Open(dir string) (*Store, error)`

Package-level function. Steps:
1. Build `tqDir = filepath.Join(dir, ".tq")`.
2. Stat `tqDir` → if missing, return `ErrNotInitialized`.
3. Read and parse `config.json`; validate `id_prefix` with `validatePrefix`.
4. Return `&Store{tqDir: tqDir, now: time.Now}`.

For tests, use an unexported constructor `newStoreForTest(tqDir string, now func() time.Time) *Store` that bypasses `Open` and injects a fake clock directly.

### 2d. Internal helpers

```go
// readIssue reads and parses a single issue file.
// If the file does not exist, returns ErrNotFound.
// If the file exists but fails to parse or has a filename/ID mismatch, returns ErrCorruptFile.
func (s *Store) readIssue(id string) (*Issue, error)

// writeIssue atomically writes an issue to .tq/issues/{id}.json.
// Uses tmp file + fsync + rename + dir fsync sequence from DESIGN.md §Implementation Notes #4.
func (s *Store) writeIssue(issue *Issue) error

// readAll reads all issue files in .tq/issues/.
// Skips files ending in .tmp.
// Returns the successfully parsed issues and a slice of errors for skipped files.
// Never returns an error itself — a fully unreadable directory returns (nil, nil).
func (s *Store) readAll() ([]*Issue, []error)

// resolveID resolves a partial or full ID to a canonical full ID.
// Resolution order:
//   1. Exact match against full IDs.
//   2. Prefix match: strings.HasPrefix(id, partial).
//   3. Hex-suffix prefix match: strings.HasPrefix(hexSuffix(id), partial)
//      where hexSuffix extracts everything after the last "-".
// Returns ErrNotFound if no match, ErrAmbiguousID if multiple matches.
func (s *Store) resolveID(partial string) (string, error)
```

`readAll` should call `readIssue` for each `.json` file in the issues directory. Skipped files have their errors collected but not returned — callers (CLI) print skipped-file warnings to stderr separately.

`resolveID` calls `readAll` internally to get all IDs; it does not load full issues (just filenames). Alternatively, use `filepath.Glob` on `.tq/issues/*.json` and extract IDs from filenames (stripping `.json`). The filename-based approach is faster and correct since file name must match ID (validated on read).

### 2e. File locking

```go
// withLock acquires an exclusive flock on .tq/lock, runs fn, then releases.
// Creates the lock file if it does not exist.
func (s *Store) withLock(fn func() error) error
```

Implementation using `syscall.Flock`:
```go
lockPath := filepath.Join(s.tqDir, "lock")
f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
// ...
if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { ... }
defer f.Close() // closing releases the lock
return fn()
```

Note: `syscall.Flock` is available on Linux, macOS, and WSL. It is not available on native Windows. This is acceptable per DESIGN.md §Concurrency.

### 2f. `store_test.go` — Step 2 tests

Use `t.TempDir()` for all test directories. Use `newStoreForTest` to inject a fixed clock.

| Test | What it checks |
|---|---|
| `TestInit` | `.tq/`, `.tq/issues/`, `.tq/config.json`, `.tq/.gitignore` all created; config has correct prefix and version |
| `TestInitDuplicate` | Second `Init` on same dir returns `ErrAlreadyInit` |
| `TestInitInvalidPrefix` | Prefix `"A"` returns `ErrInvalidPrefix` |
| `TestOpen` | Opens after init; prefix is read correctly |
| `TestOpenNotFound` | `Open` on a dir with no `.tq/` returns `ErrNotInitialized` |
| `TestReadWriteRoundTrip` | Write an issue, read it back, fields match |
| `TestReadAllEmpty` | Empty issues dir returns empty slice, no errors |
| `TestReadAllSkipsTemp` | A `.tmp` file in issues dir is skipped |
| `TestReadAllSkipsCorrupt` | A non-JSON file returns it in the errors slice |
| `TestResolveIDExact` | Full ID resolves to itself |
| `TestResolveIDPrefix` | `"orca-a1b"` resolves to `"orca-a1b2"` when unambiguous |
| `TestResolveIDHexSuffix` | `"a1b"` resolves to `"orca-a1b2"` when unambiguous |
| `TestResolveIDNotFound` | Unknown ID returns `ErrNotFound` |
| `TestResolveIDAmbigiuous` | Partial matching two IDs returns `ErrAmbiguousID` |

**Verification:** `go test ./...`

---

## Step 3: Store — Operations

**Files:** `store.go` (add methods), `store_test.go` (add tests)

**Goal:** All eight business-logic store methods. `DepAdd`/`DepRemove`/`DepList`/`Ready`/`Doctor` come in later steps.

### 3a. Method signatures

All mutations call `withLock`. All reads do not.

```go
func (s *Store) Create(opts CreateOpts) (*Issue, error)
func (s *Store) Show(id string) (*Issue, error)
func (s *Store) List(filter ListFilter) ([]*Issue, error)
func (s *Store) Claim(id string, actor string) (*Issue, error)
func (s *Store) Update(id string, opts UpdateOpts) (*Issue, error)
func (s *Store) Close(id string, reason string, actor string) (*Issue, error)
func (s *Store) Reopen(id string) (*Issue, error)
func (s *Store) Comment(id string, author string, text string) (*Issue, error)
```

### 3b. `Create`

1. Validate `opts.Title` non-empty → `ErrTitleRequired`.
2. Resolve priority: `opts.Priority == nil` → use `DefaultPriority`; else validate with `validatePriority`.
3. Resolve type: `opts.Type == ""` → `DefaultType`.
4. Generate ID (loop on collision: re-call `generateID` if file already exists). Read the prefix from config (store it on `Store` at `Open` time).
5. Build `Issue` with `CreatedAt = s.now().UTC().Format(time.RFC3339)`, `UpdatedAt = CreatedAt`, `Status = StatusOpen`.
6. Call `writeIssue` inside `withLock`.
7. Return the created issue.

Store the config prefix on the `Store` struct at `Open` time: add `prefix string` field to `Store`.

### 3c. `Show`

1. Call `resolveID(id)` to get canonical ID.
2. Call `readIssue(canonicalID)`.
3. Return issue or error (including `ErrCorruptFile`, `ErrNotFound`, `ErrAmbiguousID`).

No locking (read-only).

### 3d. `List`

1. Call `readAll()` — ignore per-file errors (CLI will warn separately).
2. Apply `ListFilter`:
   - `Status`: if empty, exclude `StatusClosed`; otherwise include only issues whose status is in the list.
   - `Label`: all labels in the filter must appear in `issue.Labels`.
   - `Assignee`: exact string match (empty filter = no filtering).
   - `Priority`: exact int match (`nil` = no filtering).
3. Sort: priority ASC, then `CreatedAt` ASC, then ID ASC.
4. Return filtered, sorted slice.

No locking (read-only).

### 3e. `Claim`

Preconditions (checked inside lock):
1. Resolve ID.
2. Read issue → `ErrCorruptFile` if corrupt.
3. Status must be `open` → else `ErrAlreadyClaimed`.
4. All `BlockedBy` IDs must resolve to closed issues → else `ErrBlocked`. For each blocker ID:
   - Try to read it via `readIssue`.
   - If missing or corrupt → blocker is unresolved → `ErrBlocked`.
   - If status is not `StatusClosed` → `ErrBlocked`.
5. Set `Status = StatusInProgress`, `Assignee = actor`, `UpdatedAt = now`.
6. Write issue.

The `ErrBlocked` error message should list unresolved blockers. The store method wraps the error with context; the CLI formats it for display.

### 3f. `Update`

Inside lock:
1. Resolve ID, read issue.
2. For each non-nil field in `UpdateOpts`:
   - `Status`: call `validateUpdateStatusTransition(current, *opts.Status)` → apply if valid.
   - `Priority`: validate with `validatePriority`.
   - `Assignee`: set to `*opts.Assignee` (empty string clears the field).
   - `Description`: set to `*opts.Description` (empty string clears the field).
   - `AddLabels`: append to `Labels`, deduplicating.
   - `RemoveLabels`: remove from `Labels`.
3. Set `UpdatedAt = now`.
4. Write issue.

Status constraint for closed issues: if the issue's current status is `StatusClosed` and `opts.Status != nil`, return `ErrInvalidStatus` (regardless of the new value — reopen is the only path out of closed).

### 3g. `Close`

Inside lock:
1. Resolve ID, read issue.
2. Status must be `open` or `in_progress` → else `ErrInvalidStatus`.
3. Set `Status = StatusClosed`, `ClosedAt = now`, `UpdatedAt = now`.
4. If `reason != ""`: set `CloseReason = reason`.
5. If `actor != ""`: set `Assignee = actor`.
6. Write issue.

### 3h. `Reopen`

Inside lock:
1. Resolve ID, read issue.
2. Status must be `closed` → else `ErrInvalidStatus`.
3. Set `Status = StatusOpen`, clear `ClosedAt = ""`, clear `CloseReason = ""`.
4. Do **not** clear `Assignee`.
5. Set `UpdatedAt = now`.
6. Write issue.

### 3i. `Comment`

Inside lock:
1. Resolve ID, read issue.
2. Append `Comment{Author: author, Text: text, CreatedAt: s.now().UTC().Format(time.RFC3339)}` to `issue.Comments`.
3. Set `UpdatedAt = now`.
4. Write issue.

### 3j. `store_test.go` — Step 3 tests

Use `t.TempDir()`, `Init`, then `newStoreForTest` with a fixed clock throughout.

| Test | What it checks |
|---|---|
| `TestCreateShow` | Round-trip: create with all fields, show returns same data; ID format correct |
| `TestCreateDefaults` | Nil priority → 2; empty type → "task" |
| `TestCreateTitleRequired` | Empty title → `ErrTitleRequired` |
| `TestListDefault` | Returns open and in_progress issues; excludes closed |
| `TestListFilterStatus` | `Status: []string{"closed"}` returns only closed |
| `TestListFilterLabel` | Multi-label filter: all must match |
| `TestListFilterAssignee` | Exact match |
| `TestListFilterPriority` | Only matching priority |
| `TestListSortOrder` | Priority 0 before 1; same priority sorted by created_at then ID |
| `TestListSkipsCorrupt` | Malformed file in issues dir is skipped; valid issues returned |
| `TestClaim` | Open issue → in_progress; assignee set; updated_at bumped |
| `TestClaimAlreadyClaimed` | in_progress issue → `ErrAlreadyClaimed` |
| `TestClaimClosed` | Closed issue → `ErrAlreadyClaimed` |
| `TestClaimBlocked` | Open issue with open blocker → `ErrBlocked` |
| `TestClaimBlockedOrphan` | Open issue with non-existent blocker → `ErrBlocked` |
| `TestClaimBlockedCorruptBlocker` | Open issue with corrupt blocker file → `ErrBlocked` |
| `TestClaimUnblocked` | Open issue with closed blocker → succeeds |
| `TestUpdateDescription` | Set description; updated_at changes |
| `TestUpdateDescriptionClear` | `--description ""` clears field |
| `TestUpdateAssigneeClear` | `--assignee ""` clears field |
| `TestUpdatePriority` | Valid priority set; invalid → `ErrInvalidPriority` |
| `TestUpdateLabels` | Add and remove labels; deduplication on add |
| `TestUpdateStatusInProgress` | `opts.Status = ptr("in_progress")` → `ErrInvalidStatus` |
| `TestUpdateStatusClosed` | `opts.Status = ptr("closed")` → `ErrInvalidStatus` |
| `TestUpdateStatusOpenFromOpen` | `opts.Status = ptr("open")` when already open → `ErrInvalidStatus` |
| `TestUpdateStatusOpenFromInProgress` | `opts.Status = ptr("open")` when in_progress → succeeds |
| `TestUpdateOnClosedIssue` | Can update priority/labels/description/assignee; status change → `ErrInvalidStatus` |
| `TestUpdateOnCorrupt` | `ErrCorruptFile` |
| `TestClose` | Sets status, closed_at, updated_at |
| `TestCloseWithReason` | Sets close_reason |
| `TestCloseWithActor` | Sets assignee |
| `TestCloseAlreadyClosed` | `ErrInvalidStatus` |
| `TestReopen` | Clears closed_at, close_reason; sets status to open; preserves assignee |
| `TestReopenNonClosed` | open/in_progress → `ErrInvalidStatus` |
| `TestComment` | Appends comment; updated_at bumped |
| `TestCommentOnCorrupt` | `ErrCorruptFile` |

**Verification:** `go test ./...`

---

## Step 4: Ready Computation and Dependency Operations

**Files:** `ready.go`, `store.go` (add dep methods), `ready_test.go`, `store_test.go` (add dep tests)

**Goal:** Ready algorithm, cycle detection, and dependency CRUD. All as pure functions where possible, store methods where I/O is needed.

### 4a. `ready.go` — pure functions

```go
// computeReady returns the ready subset of issues using only the provided slice.
// No I/O. Caller passes all issues from readAll().
// An issue is ready when: status == open AND all blocked_by IDs resolve to closed issues.
// Issues with missing or corrupt blockers are NOT ready (orphan blockers block readiness).
// Sort: priority ASC, created_at ASC, id ASC.
func computeReady(all []*Issue) []*Issue

// hasCycle reports whether adding the edge (from → to) would create a cycle
// in the dependency graph defined by all.
// "from is blocked by to" — so we check if "from" is reachable from "to"
// via blocked_by edges. If yes, adding this edge would create a cycle.
// Considers all issues regardless of status.
func hasCycle(all []*Issue, from, to string) bool
```

`computeReady` builds a map of id → issue for O(1) lookup, then iterates candidates.

`hasCycle` does a BFS/DFS from `to` following `blocked_by` edges. If `from` is reached, a cycle exists.

### 4b. `(s *Store) Ready() ([]*Issue, error)`

```go
func (s *Store) Ready() ([]*Issue, error) {
    all, _ := s.readAll()  // ignore per-file errors; CLI warns separately
    return computeReady(all), nil
}
```

No locking (read-only).

### 4c. `(s *Store) DepAdd(id, blockedBy string) error`

Inside lock:
1. Resolve `id` and `blockedBy` separately → `ErrNotFound` for either.
2. `id == blockedBy` → `ErrSelfDep`.
3. Read issue `id`.
4. Check if `blockedBy` is already in `issue.BlockedBy` → `ErrDupDep`.
5. Read all issues; call `hasCycle(all, id, blockedBy)` → `ErrCycleDetected` if true.
6. Append `blockedBy` to `issue.BlockedBy`, set `UpdatedAt = now`, write issue.

### 4d. `(s *Store) DepRemove(id, blockedBy string) error`

Inside lock:
1. Resolve `id` and `blockedBy` → `ErrNotFound` for either.
2. Read issue `id`.
3. Check `blockedBy` is in `issue.BlockedBy` → `ErrDepNotFound` if not.
4. Remove from `issue.BlockedBy`, set `UpdatedAt = now`, write issue.

After removal, if `BlockedBy` is empty, the slice becomes nil (so `omitempty` omits the field in JSON).

### 4e. `(s *Store) DepList(id string) (*DepGraph, error)`

No locking (read-only):
1. Resolve `id`, read issue `id` → populate `BlockedBy` field by reading each blocker issue.
2. Call `readAll()`, scan for issues whose `BlockedBy` contains `id` → these are the `Blocks` list.
3. Return `&DepGraph{BlockedBy: ..., Blocks: ...}`.

### 4f. `ready_test.go` — pure function tests

Table-driven, zero I/O:

| Test case | Expected |
|---|---|
| No issues | empty |
| All open, no deps | all in result, sorted by priority |
| One open with open blocker | not ready |
| One open with closed blocker | ready |
| Chain: A blocked by B blocked by C; C closed, B open | A not ready |
| Multiple blockers, some closed some open | not ready |
| `in_progress` issue | excluded from result |
| Closed issue | excluded from result |
| Orphan blocker (non-existent ID) | not ready |
| Sort: priority 0 before 1; same priority: older created_at first; same created_at: lexicographic ID |

Cycle detection table:

| Case | Expected |
|---|---|
| No existing edges; add A→B | no cycle |
| Add B→A when A→B exists | cycle |
| Add C→A when A→B→C exists | cycle |
| Add A→A | cycle (self; though `DepAdd` catches `ErrSelfDep` first, hasCycle should still report it) |
| Cycle through closed issues | cycle (structural, status-independent) |

### 4g. `store_test.go` — dep tests

| Test | What it checks |
|---|---|
| `TestDepAdd` | Adds blocker; file updated |
| `TestDepAddSelf` | `ErrSelfDep` |
| `TestDepAddDuplicate` | `ErrDupDep` |
| `TestDepAddNonExistentIssue` | `ErrNotFound` |
| `TestDepAddNonExistentBlocker` | `ErrNotFound` |
| `TestDepAddCycle` | `ErrCycleDetected` |
| `TestDepAddToClosedIssue` | Succeeds (structural dep) |
| `TestDepAddFromClosedIssue` | Succeeds (structural dep) |
| `TestDepRemove` | Removes blocker; field cleared to nil when empty |
| `TestDepRemoveNonExistentEdge` | `ErrDepNotFound` |
| `TestDepRemoveNonExistentIssue` | `ErrNotFound` |
| `TestDepList` | `BlockedBy` and `Blocks` both populated correctly |
| `TestReadyEndToEnd` | Full file-backed test matching design's ready algorithm |
| `TestReadyOrphanBlocker` | Issue with non-existent blocker is not in ready |
| `TestReadyMalformedFile` | Corrupt file skipped; valid ready issues returned |
| `TestReadyMalformedBlocker` | Issue with corrupt blocker file is not ready |

**Verification:** `go test ./...`

---

## Step 5: Doctor

**Files:** `store.go` (add `Doctor` method), `store_test.go` (add doctor tests)

**Goal:** All eight health checks. No new types needed.

### 5a. `(s *Store) Doctor() (*DoctorReport, error)`

Implement the eight checks from DESIGN.md §CLI Interface — `tq doctor`. Use the stable check IDs and names from the design's table.

Implement as a slice of check results, each built independently:

| Check ID | Logic |
|---|---|
| `workspace_dir` | `os.Stat(s.tqDir)` |
| `config_valid` | Re-read and re-validate `config.json` |
| `issues_dir` | `os.Stat(filepath.Join(s.tqDir, "issues"))` |
| `issue_json_valid` | For each `.json` file: parse; collect failures |
| `issue_filename_matches_id` | For each parseable issue: compare filename stem to `issue.ID` |
| `orphan_dependencies` | For each issue, for each `BlockedBy` ID: check the referenced file exists |
| `dependency_cycles` | Run `hasCycle` for every existing edge; report first cycle found |
| `stale_temp_files` | `filepath.Glob(.tq/issues/*.tmp)` |

`DoctorReport.OK` is `true` if and only if all checks have `Status == "pass"` or `Status == "warn"`.

For the cycle check, iterate all issues and all their `BlockedBy` edges. Build the full graph, then run a standard graph cycle detection (DFS with coloring). This is distinct from `hasCycle` (which checks one proposed edge) — write a separate `detectCycles(all []*Issue) []string` that returns descriptions of all cycles found.

### 5b. `store_test.go` — doctor tests

| Test | Setup | Expected |
|---|---|---|
| `TestDoctorClean` | Valid workspace, 2 issues, no issues | All checks pass, `OK == true` |
| `TestDoctorOrphanDep` | Issue A blocked by `orca-dead` (nonexistent) | `orphan_dependencies` fails |
| `TestDoctorCycle` | A→B, B→A | `dependency_cycles` fails |
| `TestDoctorCorruptJSON` | Write `"not json"` to an issue file | `issue_json_valid` fails |
| `TestDoctorFilenameMismatch` | Write valid JSON with ID `orca-aaaa` to file `orca-bbbb.json` | `issue_filename_matches_id` fails |
| `TestDoctorStaleTmp` | Create `issues/orca-xxxx.json.tmp` | `stale_temp_files` warns; `OK` still true |
| `TestDoctorOKFalseOnFail` | Any fail check | `OK == false` |
| `TestDoctorOKTrueOnWarnOnly` | Only warn checks | `OK == true` |

**Verification:** `go test ./...`

---

## Step 6: CLI

**Files:** `cmd/tq/main.go`, `cli_test.go`

**Goal:** Full CLI. Build the binary, handle all commands, format output. The CLI is a thin layer over the store — no business logic lives here.

### 6a. Workspace discovery

Implement `findWorkspace(dir string) (string, error)` in `main.go`:
1. If `dir != ""` (from `--dir` flag): use it directly.
2. Else if `TQ_DIR` is set: use `os.Getenv("TQ_DIR")`.
3. Else: walk upward from `os.Getwd()`, checking for `.tq/` at each level. Stop at root. Return `ErrNotInitialized` if not found.

This returns the workspace root (the directory containing `.tq/`), which is passed to `tq.Open`.

### 6b. Command dispatch pattern

```go
func main() {
    if len(os.Args) < 2 {
        printUsage(os.Stderr)
        os.Exit(1)
    }
    cmd := os.Args[1]
    args := os.Args[2:]
    switch cmd {
    case "init":    runInit(args)
    case "create":  runCreate(args)
    case "show":    runShow(args)
    case "list":    runList(args)
    case "ready":   runReady(args)
    case "claim":   runClaim(args)
    case "update":  runUpdate(args)
    case "close":   runClose(args)
    case "reopen":  runReopen(args)
    case "comment": runComment(args)
    case "dep":     runDep(args)
    case "doctor":  runDoctor(args)
    case "help":    runHelp(args)
    default:
        fmt.Fprintf(os.Stderr, "tq: unknown command %q\n", cmd)
        os.Exit(1)
    }
}
```

Each `run*` function:
1. Defines a `flag.FlagSet` with `flag.ContinueOnError`.
2. Defines flags including `--dir string` and `--json bool` where applicable.
3. Calls `fs.Parse(args)`; on error, writes to stderr and exits 1.
4. Opens the store (or initializes for `init`).
5. Calls the store method.
6. Formats and prints output.
7. On any error: `fmt.Fprintln(os.Stderr, err)` and `os.Exit(1)`.

`runDep` dispatches further on `args[0]` to `runDepAdd`, `runDepRemove`, `runDepList`.

### 6c. Helper: `die(format string, args ...any)`

```go
func die(format string, args ...any) {
    fmt.Fprintf(os.Stderr, "tq: "+format+"\n", args...)
    os.Exit(1)
}
```

### 6d. Human-readable formatters

Implement these private functions in `main.go` (or a separate `format.go` file inside `cmd/tq/`):

**`formatList(issues []*tq.Issue)`** — print tab-aligned table:
```
orca-a1b2  P1  bug      Fix login timeout
orca-c3d4  P2  feature  Add dark mode toggle
```
Use `text/tabwriter` for alignment.

**`formatShow(issue *tq.Issue)`** — multiline format from DESIGN.md §Human Output Formats.

**`formatDepList(graph *tq.DepGraph)`** — two sections: "Blocked by:" and "Blocks:".

**`formatDoctor(report *tq.DoctorReport)`** — one line per check: `[pass]`, `[warn]`, `[fail]` prefix with check name and message.

### 6e. JSON output

Where `--json` is supported, use `json.NewEncoder(os.Stdout).Encode(value)` — this produces compact output with a trailing newline. For lists, encode the slice directly. For `doctor`, encode `*DoctorReport`. Errors always go to stderr only.

### 6f. `cli_test.go` — integration tests

Pattern: build the binary once per `TestMain`, then each test runs it against a temp dir.

```go
var binaryPath string

func TestMain(m *testing.M) {
    tmp, _ := os.MkdirTemp("", "tq-bin-*")
    binaryPath = filepath.Join(tmp, "tq")
    if err := exec.Command("go", "build", "-o", binaryPath, "./cmd/tq/").Run(); err != nil {
        fmt.Fprintln(os.Stderr, "build failed:", err)
        os.Exit(1)
    }
    code := m.Run()
    os.RemoveAll(tmp)
    os.Exit(code)
}

func run(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
    cmd := exec.Command(binaryPath, args...)
    cmd.Dir = dir
    var out, errOut bytes.Buffer
    cmd.Stdout, cmd.Stderr = &out, &errOut
    err := cmd.Run()
    exitCode = 0
    if e, ok := err.(*exec.ExitError); ok {
        exitCode = e.ExitCode()
    }
    return out.String(), errOut.String(), exitCode
}
```

| CLI Test | What it checks |
|---|---|
| `TestCLIInitCreateShow` | Full round-trip; created ID printed; show output contains title |
| `TestCLIInitDir` | `tq init --dir PATH` creates workspace at PATH |
| `TestCLITQDIR` | `TQ_DIR=path tq create ...` uses env-specified workspace |
| `TestCLIReadyJSON` | `tq ready --json` produces valid JSON array |
| `TestCLIShowJSON` | `tq show ID --json` produces valid JSON object with correct `id` field |
| `TestCLIClaimSuccess` | Ready issue can be claimed; exit 0 |
| `TestCLIClaimAlreadyClaimed` | Already in_progress → exit 1, stderr mentions current status and assignee |
| `TestCLIClaimBlocked` | Blocked issue → exit 1, stderr mentions blocker ID |
| `TestCLIClaimOrphanBlocker` | Orphan blocker → exit 1 |
| `TestCLIUpdateStatusInProgress` | `tq update ID --status in_progress` → exit 1 |
| `TestCLIUpdateStatusClosed` | `tq update ID --status closed` → exit 1 |
| `TestCLICloseAlreadyClosed` | `tq close ID` twice → exit 1 on second |
| `TestCLIReopenNonClosed` | `tq reopen` on open issue → exit 1 |
| `TestCLIDepAddCycle` | Creating cycle → exit 1, message mentions cycle |
| `TestCLIDepListJSON` | `tq dep list ID --json` → valid JSON with `blocked_by` and `blocks` keys |
| `TestCLIMalformedFileWarning` | Corrupt file → valid JSON on stdout, warning on stderr |
| `TestCLIDoctorJSON` | `tq doctor --json` → valid JSON; each check has `id`, `name`, `status` |
| `TestCLIPartialID` | Partial ID resolves to correct issue |
| `TestCLIPartialIDAmbiguous` | Ambiguous partial ID → exit 1 |
| `TestCLIDirFlagOverrides` | `--dir` flag takes precedence over CWD discovery |

**Verification:** `go test ./...` and `./check.sh` (all three checks pass for the first time).

---

## Step 7: README

**File:** `README.md`

Content sections:
1. **What it is** — one paragraph. Point to DESIGN.md for full design rationale.
2. **Install** — `go install github.com/soenderby/task-queue/cmd/tq@latest`
3. **Quick start** — `tq init`, `tq create`, `tq ready`, `tq claim`, `tq close`
4. **Command reference** — brief table of all commands with one-line descriptions (not a full re-spec of the CLI)
5. **Agent setup** — verbatim copy of the "Recommended Agent Instructions" block from DESIGN.md
6. **Library usage** — minimal Go snippet showing `tq.Open`, `store.Ready()`, `store.Claim()`

---

## Commit Checkpoints

| Step | Commit message |
|---|---|
| 0 | `scaffold: module, directory layout, stub files` |
| 1 | `issue: types, errors, ID generation, validation` |
| 2 | `store: init, open, read/write primitives, locking` |
| 3 | `store: create, show, list, claim, update, close, reopen, comment` |
| 4 | `ready: computation, cycle detection, dep add/remove/list` |
| 5 | `doctor: workspace health checks` |
| 6 | `cli: all commands, output formatters, integration tests` |
| 7 | `docs: README` |

Each commit must pass `go vet ./...` and `go test ./...`. `./check.sh` passes fully from Step 6 onward.

---

## Implementation Notes Not in the Design

These are small decisions that don't appear in DESIGN.md and would otherwise be made ad-hoc:

**Sorting.** Use `slices.SortFunc` (available in Go 1.21+) with a comparator that first compares `Priority`, then `CreatedAt` (lexicographic is correct for RFC 3339 UTC strings), then `ID`.

**Label deduplication on `AddLabels`.** Before appending, check if the label already exists in `issue.Labels`. Skip silently if it does (no error, no duplicate stored).

**`RemoveLabels` for non-existent label.** Remove silently if label is not present. No error.

**`updateOpts` with no fields set.** Bumps `updated_at` only. Not an error. The store does not validate that at least one field was provided.

**`json.NewEncoder` vs `json.Marshal` for CLI output.** Use `json.NewEncoder(os.Stdout).Encode(v)` for streaming. Note this produces a trailing newline, which is correct for piping.

**`text/tabwriter` alignment for list/ready output.** Use tab stops at 8 or 12 characters to keep the type column from over-aligning. The exact column widths are not specified by the design; keep it visually similar to the example in DESIGN.md §Human Output Formats.

**`tq help [command]`** — in v1, printing the full usage to stdout is acceptable. Per-command help is a bonus.

**Exit code for `ErrAmbiguousID` and `ErrNotFound`.** Both exit 1. Error message to stderr.

**`--json` on commands that don't support it.** Undefined by the design. In v1, silently ignore it (the flag is not registered on that command's FlagSet, so passing it will print a usage error — acceptable behavior).
