# tq — Design Document

A task queue for coding agents. Local-first, file-based, zero dependencies.

## Purpose

tq is a minimal task tracker designed for one thing: giving coding agents a structured work queue with dependency-aware readiness computation. An agent asks "what can I work on?" and gets back a prioritized list of unblocked issues with machine-readable output.

tq is designed to be used standalone as a CLI tool, and to be importable as a Go library so that a harness like orca can embed it directly without subprocess overhead.

## Non-Goals

These are explicitly out of scope. Not deferred — excluded.

- **No database.** No SQLite, no Dolt, no embedded DB. Storage is plain files.
- **No daemon.** No background process. tq is invoked, does its work, exits.
- **No git operations.** tq reads and writes files. The caller decides when to commit. tq never runs git.
- **No network operations.** No sync, no remotes, no API.
- **No orchestration.** tq tracks issues. It does not schedule agents, manage sessions, or run anything.
- **No compaction or memory decay.** Closed issues stay as files until the user deletes them.
- **No hierarchical IDs.** No epics, no sub-tasks. Issues are flat. Use `blocked_by` for sequencing.
- **No messaging or threading.** Comments are append-only notes. No message types, no threads.
- **No MCP integration.** CLI is the interface.
- **No YAML, no markdown frontmatter.** Issue files are JSON. One format, one parser.

## Data Model

### Issue

```go
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

type Comment struct {
    Author    string `json:"author"`
    Text      string `json:"text"`
    CreatedAt string `json:"created_at"`
}
```

### Field Semantics

| Field | Required | Values / Constraints |
|---|---|---|
| `id` | yes | `{prefix}-{4hex}`, e.g. `orca-a1b2` |
| `title` | yes | non-empty string |
| `description` | no | free text |
| `status` | yes | `open`, `in_progress`, `closed` |
| `priority` | yes | 0 (critical) through 4 (backlog), default 2 |
| `type` | no | `task`, `bug`, `feature`. Default `task`. Not enforced — free string. |
| `assignee` | no | free string (typically agent name) |
| `labels` | no | list of strings. Orca uses `px:exclusive` and `ck:<key>` conventions. tq does not interpret labels. |
| `blocked_by` | no | list of issue IDs. This issue cannot be worked until all listed issues are closed. |
| `comments` | no | append-only list of `{author, text, created_at}` |
| `created_at` | yes | RFC 3339 timestamp |
| `created_by` | no | free string |
| `updated_at` | yes | RFC 3339 timestamp. Updated on every mutation. |
| `closed_at` | no | RFC 3339 timestamp. Set when status becomes `closed`. |
| `close_reason` | no | free text |

Timestamps use RFC 3339 strings (e.g. `2026-03-23T10:00:00Z`), not `time.Time`. This keeps the JSON representation simple and avoids serialization edge cases.

### Statuses

Three statuses. No custom statuses, no state machine enforcement beyond these rules:

- `open` → can transition to `in_progress` or `closed`
- `in_progress` → can transition to `open` or `closed`
- `closed` → can transition to `open` (reopen)

The `claim` operation atomically sets `status=in_progress` and `assignee=<actor>`. It is the intended way for agents to pick up work.

### Dependencies

One dependency type: **blocks**. The `blocked_by` field on issue A lists the IDs of issues that must be closed before A is ready. No other relationship types (related, parent-child, discovered-from). Use comments or description to note non-blocking relationships.

Invariants enforced by tq:
- Cannot add a dependency on a non-existent issue.
- Cannot add a self-dependency.
- Cannot create a dependency cycle. Cycle detection runs on every `dep add`.

## Errors

The library defines sentinel errors for all failure modes that callers need to distinguish programmatically. All errors are wrapped with `fmt.Errorf("...: %w", err)` to support `errors.Is()`.

```go
var (
    ErrNotFound       = errors.New("issue not found")
    ErrAmbiguousID    = errors.New("ambiguous issue ID")
    ErrAlreadyClaimed = errors.New("issue is not open")
    ErrCycleDetected  = errors.New("dependency would create cycle")
    ErrSelfDep        = errors.New("cannot depend on self")
    ErrDupDep         = errors.New("dependency already exists")
    ErrDepNotFound    = errors.New("dependency does not exist")
    ErrNotInitialized = errors.New("tq workspace not found")
    ErrAlreadyInit    = errors.New("tq workspace already exists")
    ErrInvalidStatus  = errors.New("invalid status value")
    ErrInvalidPriority = errors.New("priority must be 0-4")
    ErrTitleRequired  = errors.New("title is required")
    ErrCorruptFile    = errors.New("issue file is corrupt")
)
```

The CLI maps these to stderr messages and exit code 1. Error messages should be specific and actionable. Examples:

- `Claim` on an in_progress issue: `"cannot claim orca-a1b2: issue is not open (status: in_progress, assignee: agent-1)"`
- Ambiguous partial ID: `"ambiguous ID 'a1b': matches orca-a1b2, orca-a1b9"`
- Cycle detected: `"cannot add dependency: orca-a1b2 → orca-c3d4 would create cycle"`

## Storage

### Layout

```
.tq/
├── config.json         # project configuration
├── issues/             # one JSON file per issue
│   ├── orca-a1b2.json
│   ├── orca-c3d4.json
│   └── orca-e5f6.json
└── .gitignore          # ignores lock file
```

### Why per-file, not JSONL

- **Better git diffs.** Changing one field in one issue shows only that file changed, and the diff is one line within a pretty-printed JSON object.
- **Agents can read issues directly.** `cat .tq/issues/orca-a1b2.json` works without any tool. An agent can inspect individual issues by reading files.
- **No rewrite-the-world.** Updating one issue writes one file. A JSONL store rewrites the entire file on every mutation.
- **Natural file-level operations.** Creating an issue creates a file. Deleting an issue deletes a file. `ls .tq/issues/` is the issue list.

### Issue files

Pretty-printed JSON, 2-space indent. This optimizes for git diffs and human readability. Example:

```json
{
  "id": "orca-a1b2",
  "title": "Fix login timeout",
  "description": "The login page times out after 30 seconds instead of the configured 120.",
  "status": "open",
  "priority": 1,
  "type": "bug",
  "labels": ["ck:auth"],
  "blocked_by": ["orca-e5f6"],
  "comments": [
    {
      "author": "agent-1",
      "text": "Investigated: the timeout constant is hardcoded in middleware.",
      "created_at": "2026-03-23T10:30:00Z"
    }
  ],
  "created_at": "2026-03-23T10:00:00Z",
  "created_by": "jsk",
  "updated_at": "2026-03-23T10:30:00Z"
}
```

File name is `{id}.json`. The ID in the file must match the file name (validated on read).

### Config file

`.tq/config.json`:

```json
{
  "version": 1,
  "id_prefix": "orca"
}
```

| Field | Purpose |
|---|---|
| `version` | Schema version. Currently `1`. For future-proofing only. |
| `id_prefix` | Prefix for generated issue IDs. Set at `init` time. |

### .gitignore

Created by `tq init`:

```
lock
```

The lock file is runtime-only and must not be committed.

### Workspace Discovery

The CLI must find the `.tq/` directory before executing any command (except `init` and `help`).

**Resolution order:**
1. `--dir PATH` flag (if provided on any command) — use `PATH/.tq/` directly.
2. `TQ_DIR` environment variable (if set) — use `$TQ_DIR/.tq/` directly.
3. Walk upward from CWD: check CWD for `.tq/`, then parent, then grandparent, etc. Stop at filesystem root.

If no `.tq/` directory is found, fail with `ErrNotInitialized` and a message suggesting `tq init`.

The library `Open(dir)` function takes an explicit path and does no discovery. The caller is responsible for passing the right directory. This keeps the library simple and testable while letting the CLI handle user-facing convenience.

For orca integration: orca calls `tq.Open(primaryRepoPath)` with an explicit path. No discovery needed.

## CLI Interface

Binary name: `tq`

### Commands

```
tq init [--prefix PREFIX] [--dir PATH]
tq create TITLE [options]
tq show ID [--json]
tq list [options] [--json]
tq ready [--json]
tq claim ID --actor NAME [--json]
tq update ID [options]
tq close ID [--reason TEXT] [--actor NAME]
tq reopen ID
tq comment ID TEXT [--author NAME]
tq dep add ID BLOCKED_BY_ID
tq dep remove ID BLOCKED_BY_ID
tq dep list ID [--json]
tq doctor [--json]
tq help [command]
```

### Global Flags

These flags apply to all commands (except `help`):

- `--dir PATH` — use `PATH` as the workspace root. Overrides `TQ_DIR` and upward discovery. On `init`, this is the directory where `.tq/` will be created. On all other commands, this is where `.tq/` is expected to exist.
- `--json` — where supported, output machine-readable JSON to stdout instead of human-readable text. Not all commands support this (see individual command details).

### Command Details

#### `tq init [--prefix PREFIX] [--dir PATH]`

Initialize tq in the target directory. Creates `.tq/`, `.tq/config.json`, `.tq/issues/`, `.tq/.gitignore`.

- `--prefix`: ID prefix (default: basename of target directory)
- `--dir PATH`: target directory (default: CWD)
- Fails if `.tq/` already exists (`ErrAlreadyInit`).

#### `tq create TITLE [options]`

Create a new issue. Prints the new issue ID to stdout.

Options:
- `-d, --description TEXT` — description
- `-p, --priority N` — priority 0–4 (default: 2)
- `-t, --type TYPE` — issue type (default: `task`)
- `-l, --label LABEL` — label (repeatable)
- `-a, --assignee NAME` — assignee
- `--created-by NAME` — creator (default: empty)
- `--json` — output full issue JSON instead of just ID

#### `tq show ID [--json]`

Display a single issue.

- Default: human-readable formatted output.
- `--json`: output the issue JSON object.
- Supports partial ID matching: `tq show a1b` matches `orca-a1b2` if unambiguous. Fails with error if multiple matches.

#### `tq list [options] [--json]`

List issues, filtered and sorted.

Options:
- `--status STATUS` — filter by status (repeatable for multiple)
- `--label LABEL` — filter by label (repeatable, all must match)
- `--assignee NAME` — filter by assignee
- `--priority N` — filter by priority
- `--json` — output JSON array

Default: lists all non-closed issues, sorted by priority then created_at.

#### `tq ready [--json]`

List issues that are ready to work on. An issue is ready when:
1. Status is `open` (not `in_progress`, not `closed`)
2. Every ID in its `blocked_by` list refers to an issue with status `closed`

Output is sorted by priority (0 first), then by `created_at` (oldest first), then by ID (lexicographic tiebreak).

- Default: human-readable table.
- `--json`: JSON array of issue objects.

#### `tq claim ID --actor NAME [--json]`

Claim an issue for work. Atomically sets status to `in_progress` and assignee to NAME. This is the primary way agents pick up work.

Precondition: the issue must have status `open`. If the issue is `in_progress` or `closed`, claim fails with `ErrAlreadyClaimed` (exit code 1) and a message identifying the current status and assignee. This makes concurrent claims safe under file locking.

- `--json` — output the claimed issue JSON.

#### `tq update ID [options]`

Update fields on an issue. For claiming work, use `tq claim` instead.

Options:
- `--status STATUS` — set status
- `--priority N` — set priority
- `--assignee NAME` — set assignee
- `--add-label LABEL` — add a label (repeatable)
- `--remove-label LABEL` — remove a label (repeatable)
- `--json` — output updated issue JSON

All fields are independently optional. Only specified fields are changed. `updated_at` is set to now on every successful update.

Status changes via `update` must follow the valid transitions defined in the Statuses section. In particular, `update --status` on a closed issue fails with `ErrInvalidStatus` — use `tq reopen` to transition a closed issue back to `open`.

#### `tq close ID [--reason TEXT] [--actor NAME]`

Close an issue. Sets status to `closed`, `closed_at` to now. Optionally sets `close_reason` and `assignee`.

#### `tq reopen ID`

Reopen a closed issue. Sets status to `open`, clears `closed_at` and `close_reason`.

#### `tq comment ID TEXT [--author NAME]`

Append a comment to an issue. Comments are append-only; there is no edit or delete.

#### `tq dep add ID BLOCKED_BY_ID`

Add a blocking dependency: ID is blocked by BLOCKED_BY_ID. Fails if:
- Either issue does not exist.
- ID == BLOCKED_BY_ID (self-dependency).
- The dependency would create a cycle.
- The dependency already exists.

#### `tq dep remove ID BLOCKED_BY_ID`

Remove a blocking dependency.

#### `tq dep list ID [--json]`

List dependencies for an issue. Shows what blocks it and what it blocks.

- Default: human-readable.
- `--json`: JSON object with `blocked_by` (issues that block this one) and `blocks` (issues this one blocks).

Implementation note: the reverse lookup ("what does this issue block?") requires reading all issues to find those whose `blocked_by` list contains this ID. Use `readAll()` for this operation — do not attempt indexing or caching at this scale.

#### `tq doctor [--json]`

Run health checks on the tq workspace.

Checks:
1. `.tq/` directory exists
2. `config.json` is valid
3. `issues/` directory exists
4. All issue files parse as valid JSON
5. All issue file names match their contained ID
6. No orphan dependency references (blocked_by pointing to non-existent issue)
7. No dependency cycles

### Output Conventions

- **Human output** (default): compact, tabular where appropriate. To stderr for errors, stdout for data.
- **JSON output** (`--json`): compact single-line JSON to stdout. `show` outputs one object. `list`, `ready`, `dep list` output arrays or structured objects. Errors remain on stderr as text.
- **Exit codes**: 0 on success. 1 on error. Errors always include a message on stderr.
- **Partial ID matching**: Any command that takes an ID accepts a partial match (prefix or substring of the hex portion). Ambiguous matches (multiple issues match) fail with an error listing the matches.

### Human Output Formats

`tq list` / `tq ready`:
```
orca-a1b2  P1  bug      Fix login timeout
orca-c3d4  P2  feature  Add dark mode toggle
```

`tq show`:
```
orca-a1b2  Fix login timeout

  Status:      open
  Priority:    1
  Type:        bug
  Assignee:    (none)
  Labels:      ck:auth
  Blocked by:  orca-e5f6 (open)
  Created:     2026-03-23 by jsk

  The login page times out after 30 seconds instead of the configured 120.

  Comments:
    [2026-03-23 agent-1] Investigated: the timeout constant is hardcoded in middleware.
```

## ID Generation

Format: `{prefix}-{hex}` where:
- `prefix` is from `.tq/config.json` (set at `init` time)
- `hex` is 4 random lowercase hex characters (16 bits, 65,536 possibilities)

On collision (generated ID already exists as a file), regenerate. At orca's scale (tens of issues), collisions are vanishingly rare.

Use `crypto/rand` for hex generation, not `math/rand`.

Partial ID matching: users can refer to `a1b2` or `orca-a1b` and tq resolves it if unambiguous.

## Ready Computation

The core algorithm, expressed as pseudocode:

```
ready(all_issues) → []Issue:
    candidates = [i for i in all_issues if i.status == "open"]
    for each candidate:
        is_ready = true
        for each blocker_id in candidate.blocked_by:
            blocker = lookup(blocker_id, all_issues)
            if blocker is nil:
                continue                     # orphan dep: treat as non-blocking (doctor warns)
            if blocker.status != "closed":
                is_ready = false
                break
        if is_ready:
            add candidate to result
    sort result by (priority ASC, created_at ASC, id ASC)
    return result
```

Key decisions:
- Only `open` issues are candidates. `in_progress` means already claimed — not ready for another agent.
- Orphan dependencies (blocked_by referencing a deleted/non-existent issue) are treated as resolved, not blocking. `tq doctor` reports these as warnings.
- Sort is deterministic: priority first (lower number = higher priority), then creation time (older first), then ID (lexicographic tiebreak).

## Cycle Detection

On `tq dep add A B` (A is blocked by B), detect whether adding this edge would create a cycle in the dependency graph.

Algorithm: starting from B, traverse `blocked_by` edges recursively (or BFS). If A is reachable from B, adding A→B would create a cycle. Reject with `ErrCycleDetected`.

This is O(n) in the number of issues, which is fine for orca's scale.

Cycle detection considers **all issues regardless of status**. A cycle like A (open) → B (closed) → C (open) → A is still rejected, even though B is closed and doesn't currently block anything. Rationale: closed issues can be reopened, and a structural cycle that becomes live on reopen is a latent hazard. The dependency graph is a structural invariant, not a runtime state.

Only `dep add` performs cycle detection. `doctor` also reports cycles as a diagnostic.

## Edge Cases

Explicit behavior for cases that implementing agents must not invent answers for:

**Closing an issue that blocks others.** Nothing cascades. The blocked issues are not modified. They simply become unblocked — their `blocked_by` list now points to a closed issue, which `ready` treats as resolved. No automatic status transitions, no notifications.

**Adding a dependency to or from a closed issue.** Allowed. The dependency is structural. A closed issue may be reopened later, and the dependency should be in place when it is. Cycle detection still applies regardless of issue status.

**Updating fields on a closed issue.** Allowed for all fields except status (use `reopen` for that). Priority, labels, assignee, and description can be changed on closed issues. `updated_at` is set to now.

**Malformed issue file.** If a file in `.tq/issues/` contains invalid JSON or fails ID validation, `list`, `ready`, and other multi-issue operations **skip the malformed file** and emit a warning to stderr. They do not fail entirely — one corrupt file should not block all operations. `doctor` reports malformed files as errors. `show` on the specific malformed issue returns `ErrCorruptFile`.

**Empty issues directory.** `ready`, `list`, and `dep list` return empty results, not errors.

**Reopening an issue.** Sets status to `open`. Clears `closed_at` and `close_reason`. Does **not** clear `assignee` — the previous assignee may want to continue the work. To clear the assignee, use `tq update ID --assignee ""`.

**Claiming an already-claimed issue.** Fails with `ErrAlreadyClaimed`. The error message includes the current status and assignee so the caller knows who holds it. The caller must pick a different issue or wait.

**Duplicate dependency.** `dep add A B` when A already has B in `blocked_by` fails with `ErrDupDep`. This is not silent — the caller should know the state didn't change.

**Removing a non-existent dependency.** `dep remove A B` when B is not in A's `blocked_by` fails with `ErrDepNotFound`.

## Concurrency

tq uses file locking for safe concurrent access. The lock file is `.tq/lock`.

### Mutation operations (create, claim, update, close, reopen, comment, dep add, dep remove):

1. Open `.tq/lock` for exclusive write (flock LOCK_EX)
2. Read relevant issue file(s)
3. Validate and apply mutation
4. Write updated file atomically (write to `.tq/issues/{id}.json.tmp`, then rename to `.tq/issues/{id}.json`)
5. Close lock file (releases lock)

### Read operations (show, list, ready, dep list):

No locking required. Reads are not transactional — they see whatever is on disk. At orca's scale and access pattern (one writer at a time via orca's own lock), this is safe. If a read races with a write, it gets either the old or new state of the file (atomic rename guarantees no partial reads).

### Orca integration

When orca embeds tq as a library, orca's own lock-guarded write path on main provides the outer serialization. tq's internal file lock provides a safety net but is not the primary concurrency control. The two lock layers compose safely (tq lock is always acquired inside orca lock, never the reverse).

## Project Structure

```
tq/
├── cmd/tq/
│   └── main.go               # CLI entrypoint, command dispatch, flag parsing
├── issue.go                   # Issue and Comment types, validation, ID generation
├── store.go                   # Store type: init, open, read, write, locking
├── ready.go                   # Ready computation and cycle detection
├── issue_test.go              # Validation, ID tests
├── store_test.go              # Store operations with temp directories
├── ready_test.go              # Ready computation, cycle detection tests
├── cli_test.go                # Integration tests: run binary, check output
├── DESIGN.md
├── README.md
├── go.mod
└── check.sh                   # go vet + go test gate
```

The module root package (`tq`) is the public library API. `cmd/tq/` is a thin CLI wrapper.

### Public API Surface (Library)

```go
package tq

// Store manages a .tq workspace.
type Store struct { ... }

// Init creates a new .tq workspace at dir.
func Init(dir string, prefix string) error

// Open opens an existing .tq workspace at dir.
func Open(dir string) (*Store, error)

// Issue operations
func (s *Store) Create(opts CreateOpts) (*Issue, error)
func (s *Store) Show(id string) (*Issue, error)      // supports partial ID
func (s *Store) List(filter ListFilter) ([]*Issue, error)
func (s *Store) Ready() ([]*Issue, error)
func (s *Store) Claim(id string, actor string) (*Issue, error)
func (s *Store) Update(id string, opts UpdateOpts) (*Issue, error)
func (s *Store) Close(id string, reason string, actor string) (*Issue, error)
func (s *Store) Reopen(id string) (*Issue, error)
func (s *Store) Comment(id string, author string, text string) (*Issue, error)

// Dependency operations
func (s *Store) DepAdd(id string, blockedBy string) error
func (s *Store) DepRemove(id string, blockedBy string) error

// Diagnostics
func (s *Store) Doctor() (*DoctorReport, error)

// All reads and writes in these methods handle file locking internally.
```

### Option Types

```go
type CreateOpts struct {
    Title       string
    Description string
    Priority    int      // 0-4, default 2
    Type        string   // default "task"
    Labels      []string
    Assignee    string
    CreatedBy   string
}

type UpdateOpts struct {
    Status       *string  // nil = don't change
    Priority     *int
    Assignee     *string
    AddLabels    []string
    RemoveLabels []string
}

type ListFilter struct {
    Status   []string // empty = all non-closed
    Label    []string // all must match
    Assignee string
    Priority *int
}

type DoctorReport struct {
    Checks []DoctorCheck `json:"checks"`
    OK     bool          `json:"ok"`
}

type DoctorCheck struct {
    Name    string `json:"name"`
    Status  string `json:"status"`  // "pass", "warn", "fail"
    Message string `json:"message"`
}
```

## Testing Strategy

### Unit tests (no I/O)

- **ID generation**: uniqueness, format, prefix correctness.
- **Issue validation**: required fields, status transitions, priority range.
- **Ready computation**: table-driven tests with pre-built issue slices. Cases:
  - No issues → empty
  - All open, no deps → all ready, sorted by priority
  - Blocked by open issue → not ready
  - Blocked by closed issue → ready
  - Chain: A blocked by B blocked by C; C closed, B open → A not ready
  - Multiple blockers, some closed some open → not ready
  - `in_progress` issues excluded from ready
  - Closed issues excluded from ready
  - Orphan dependency (non-existent blocker) → treated as ready
  - Sort order: priority, then created_at, then id
- **Cycle detection**: table-driven. Cases:
  - No cycle → allowed
  - Direct cycle (A→B, B→A) → rejected
  - Indirect cycle (A→B→C→A) → rejected
  - Self-dependency → rejected
  - Cycle through closed issues → still rejected (the graph is structural, not status-dependent)

### Store tests (temp directories)

- **Init**: creates correct directory structure and config
- **Init duplicate**: fails when `.tq/` exists (`ErrAlreadyInit`)
- **Create + Show**: round-trip
- **Create**: file appears in `.tq/issues/`, correct JSON, correct ID format
- **Show partial ID**: resolves unique prefix, fails on ambiguous prefix
- **Show malformed file**: returns `ErrCorruptFile`
- **List**: filtering by status, label, assignee, priority
- **List default**: excludes closed issues
- **List with malformed file**: skips corrupt file, returns valid issues, emits warning
- **Update**: changes fields, updates `updated_at`
- **Update closed issue**: can change priority, labels, assignee
- **Update status on closed issue**: fails with `ErrInvalidStatus`
- **Claim**: sets status + assignee, fails on non-open issue (ErrAlreadyClaimed with current status/assignee)
- **Claim already in_progress**: fails with ErrAlreadyClaimed
- **Close**: sets status, closed_at, close_reason
- **Close with actor**: sets assignee
- **Reopen**: clears closed_at, close_reason, sets status to open, preserves assignee
- **Comment**: appends to comments list
- **Dep add**: adds to blocked_by, fails on cycle, fails on self, fails on non-existent, fails on duplicate (`ErrDupDep`)
- **Dep add to/from closed issue**: succeeds (deps are structural)
- **Dep remove**: removes from blocked_by
- **Dep remove non-existent**: fails with `ErrDepNotFound`
- **Ready**: end-to-end with real files
- **Ready with malformed file**: skips corrupt file, returns valid ready issues
- **Doctor**: detects orphan deps, cycles, invalid files, malformed JSON

### CLI tests

Build the binary, run it against a temp directory, check exit codes and stdout/stderr. These test the command parsing and output formatting layer — they should not duplicate the store test logic.

- `tq init` + `tq create` + `tq show` round-trip
- `tq init --dir PATH` creates workspace at PATH
- `tq ready --json` output is valid JSON array
- `tq show --json` output is valid JSON object
- `tq claim` on already-claimed issue exits non-zero with status and assignee in message
- `tq claim` on open issue succeeds
- `tq update --status in_progress` on closed issue exits non-zero
- `tq dep add` cycle rejection exits non-zero with clear message
- `tq doctor --json` output schema
- Partial ID matching in commands
- `--dir` flag overrides workspace discovery
- `TQ_DIR` env var overrides workspace discovery

### What not to test

- No mocking. The store uses real files in temp directories.
- No testing of Go stdlib behavior (JSON marshaling, file I/O).
- No benchmarks. Performance is not a concern at orca's scale.

## Orca Integration Path

When orca's Go rewrite reaches the queue layer:

1. Add `tq` as a Go module dependency.
2. Orca's `internal/queue` package wraps `tq.Store`:
   - `queue.ReadReady()` calls `store.Ready()`
   - `queue.Claim()` calls `store.Claim()`
   - All operations happen against the primary repo's `.tq/` directory
3. Orca's lock-guarded write path becomes: acquire orca lock → switch to main → call tq store method → commit `.tq/` changes → push → release lock.
4. The `.tq` source-branch guard replaces the `.beads` guard: `merge-main.sh` rejects branches carrying `.tq/` changes.
5. Agent prompt changes `br ready --json` → `orca queue ready --json` (or equivalent subcommand).

The `tq` library knows nothing about git, branches, worktrees, or orca. The orca wrapper adds all of that.

### CLI subcommands in orca

For agents that need to call queue operations via CLI (before full library integration), orca can expose `orca queue <tq-command>` subcommands that delegate to the tq library with orca's locking and git semantics wrapped around them. This replaces the current `queue-write-main.sh` / `queue-read-main.sh` helpers.

## Recommended Agent Instructions

Projects using tq should include the following in their AGENTS.md or equivalent context file. This gives agents the minimum viable workflow without requiring them to read the full documentation.

```markdown
## Task Queue

This project uses `tq` for task tracking. Run `tq help` for available commands.

Workflow:
1. `tq ready --json` — see available work, sorted by priority
2. `tq claim <id> --actor <your-name>` — claim an issue before starting work
3. Work on the issue
4. `tq comment <id> "summary of what was done" --author <your-name>` — record progress
5. `tq close <id> --reason "description of outcome"` — close when complete

When you discover work that should be done but is outside the current task scope,
create a follow-up issue:
  `tq create "title" -d "description" -p 2`

If the follow-up blocks the current issue, add a dependency:
  `tq dep add <current-id> <new-id>`

Do not edit files in `.tq/` directly. Use `tq` commands for all mutations.
```

This block is intentionally short. Per the context engineering research, agent instructions should be concise and action-oriented. The workflow (ready → claim → work → close) is the essential pattern. Everything else (dep management, priority semantics, label conventions) is project-specific and belongs in project-level documentation, not in the generic agent instructions.

## Design Decisions Log

### Why Claim is a separate operation, not a flag on Update
Claim has unique preconditions (status must be `open`), unique side effects (sets both status and assignee atomically), and is the single most important operation for agent coordination. Bundling it into a general-purpose Update struct creates ambiguity about precedence when Claim and Status are both set, and forces implementers to handle interaction rules that don't need to exist. A separate `Claim(id, actor)` method is simpler, more predictable, and independently testable.

### Why no replace-all for labels
An earlier draft included a `Labels []string` field on UpdateOpts where `nil` meant "don't change" and empty meant "clear all." This nil-vs-empty distinction is a footgun in Go and a source of subtle bugs. `AddLabels` and `RemoveLabels` are explicit, unambiguous, and compose naturally when the CLI has multiple `--add-label` and `--remove-label` flags.

### Why JSON files, not JSONL
JSONL requires rewriting the entire file on any issue update. Per-file JSON gives per-issue git history, per-issue atomic writes, and direct file access for agents. See Storage section.

### Why no SQLite
SQLite adds a binary dependency and a file that produces meaningless git diffs. At orca's scale (tens of issues), scanning a directory of JSON files is sub-millisecond. SQLite's query advantages only matter at hundreds or thousands of issues.

### Why three statuses, not more
Every additional status is a state transition to test and a concept to explain to agents. `open` / `in_progress` / `closed` covers the full lifecycle. "blocked" is computed from dependencies, not stored as a status.

### Why one dependency type
Orca's planner only uses `blocks` for ready computation. `related`, `parent-child`, and `discovered-from` add schema complexity without affecting the ready algorithm. Non-blocking relationships can be noted in comments.

### Why tq doesn't touch git
Separation of concerns. tq manages task data. The caller (human or orca) manages version control. This keeps tq testable without git, usable in non-git contexts, and avoids the auto-commit behavior that multiple beads users cite as a pain point.

### Why not build this into orca directly
The task queue is a general-purpose primitive. Building it as a standalone tool means: (a) it can be tested and validated independently, (b) it can be used outside orca, (c) orca's complexity doesn't bleed into the task layer. If it proves useful, orca imports the library. If it doesn't, orca can switch to something else without gutting its internals.

## Implementation Sequence

Build in this order. Each step produces a `go test ./...`-passing commit.

### Step 1: Types, Errors, and Validation
- `issue.go`: Issue, Comment, CreateOpts, UpdateOpts, ListFilter types
- `issue.go`: sentinel errors (ErrNotFound, ErrAlreadyClaimed, ErrCycleDetected, etc.)
- `issue.go`: ID generation (prefix + 4 hex via crypto/rand)
- `issue.go`: validation functions (required fields, status values, priority range, status transitions)
- `issue_test.go`: table-driven validation tests, ID format tests

### Step 2: Store — Init, Read, Write
- `store.go`: Store type, Init(), Open()
- `store.go`: internal readIssue(), writeIssue(), readAll(), resolveID() (partial matching)
- `store.go`: file locking (flock wrapper)
- `store_test.go`: init, duplicate init, read/write round-trip, partial ID resolution

### Step 3: Store — Operations
- `store.go`: Create(), Show(), List(), Claim(), Update(), Close(), Reopen(), Comment()
- `store_test.go`: all operation tests (create+show round-trip, list filtering, claim semantics including ErrAlreadyClaimed, update on open and closed issues, close/reopen, comment append)

### Step 4: Ready and Cycle Detection
- `ready.go`: Ready() computation, hasCycle() detection
- `store.go`: DepAdd(), DepRemove() (using cycle detection)
- `ready_test.go`: table-driven ready computation and cycle detection tests
- `store_test.go`: dep add/remove tests with real files

### Step 5: Doctor
- `store.go`: Doctor() diagnostics
- `store_test.go`: doctor tests (orphan deps, cycles, invalid files)

### Step 6: CLI
- `cmd/tq/main.go`: command dispatch, flag parsing, output formatting
- Human-readable formatters for show, list, ready
- JSON output mode
- `cli_test.go`: build binary, run against temp dirs, check output

### Step 7: README
- Usage documentation
- Agent setup instructions (what to put in AGENTS.md)
- Examples

## Open Questions

These are deferred decisions that can be revisited after initial implementation:

1. **Should `ready` include an `--include-in-progress` flag?** Currently `ready` only returns `open` issues. Orca's planner might want to see `in_progress` issues too for status reporting. Defer until orca integration reveals the need.

2. **Should `tq` support bulk operations?** e.g., `tq close --all-ready`. Probably not — the agent or harness can loop. Defer unless a real workflow pain point emerges.

3. **Archive/purge for old closed issues?** Currently closed issues stay as files forever. A `tq archive` command could move them to `.tq/archive/`. Defer until file count becomes a problem (unlikely at orca's scale).
