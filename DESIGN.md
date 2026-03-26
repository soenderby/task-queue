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

## CLI Interface

Binary name: `tq`

### Commands

```
tq init [--prefix PREFIX]
tq create TITLE [options]
tq show ID [--json]
tq list [options] [--json]
tq ready [--json]
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

### Command Details

#### `tq init [--prefix PREFIX]`

Initialize tq in the current directory. Creates `.tq/`, `.tq/config.json`, `.tq/issues/`, `.tq/.gitignore`.

- `--prefix`: ID prefix (default: basename of current directory)
- Fails if `.tq/` already exists.

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

#### `tq update ID [options]`

Update fields on an issue.

Options:
- `--status STATUS` — set status
- `--priority N` — set priority
- `--assignee NAME` — set assignee
- `--claim --actor NAME` — atomic claim: sets status to `in_progress` and assignee to NAME. Fails if status is not `open`.
- `-l, --label LABEL` — replace labels (repeatable; pass once per label)
- `--add-label LABEL` — add a label (repeatable)
- `--remove-label LABEL` — remove a label (repeatable)
- `--json` — output updated issue JSON

The `--claim` flag is the primary way agents pick up work. It enforces that the issue is currently `open`. If the issue is already `in_progress` or `closed`, claim fails with a non-zero exit code and a clear error message. This makes concurrent claims safe under file locking.

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
                is_ready = true              # orphan dep: treat as non-blocking (doctor warns)
                continue
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

Algorithm: starting from B, traverse `blocked_by` edges recursively (or BFS). If A is reachable from B, adding A→B would create a cycle. Reject with error.

This is O(n) in the number of issues, which is fine for orca's scale.

Only `dep add` performs cycle detection. `doctor` also reports cycles as a diagnostic.

## Concurrency

tq uses file locking for safe concurrent access. The lock file is `.tq/lock`.

### Mutation operations (create, update, close, reopen, comment, dep add, dep remove):

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
    Labels       []string // nil = don't change, empty = clear
    AddLabels    []string
    RemoveLabels []string
    Claim        bool     // if true, requires Actor
    Actor        string   // used with Claim
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
- **Init duplicate**: fails when `.tq/` exists
- **Create + Show**: round-trip
- **Create**: file appears in `.tq/issues/`, correct JSON, correct ID format
- **Show partial ID**: resolves unique prefix, fails on ambiguous prefix
- **List**: filtering by status, label, assignee, priority
- **List default**: excludes closed issues
- **Update**: changes fields, updates `updated_at`
- **Claim**: sets status + assignee, fails on non-open issue
- **Close**: sets status, closed_at, close_reason
- **Reopen**: clears closed_at, close_reason, sets status to open
- **Comment**: appends to comments list
- **Dep add**: adds to blocked_by, fails on cycle, fails on self, fails on non-existent
- **Dep remove**: removes from blocked_by
- **Ready**: end-to-end with real files
- **Doctor**: detects orphan deps, cycles, invalid files

### CLI tests

Build the binary, run it against a temp directory, check exit codes and stdout/stderr. These test the command parsing and output formatting layer — they should not duplicate the store test logic.

- `tq init` + `tq create` + `tq show` round-trip
- `tq ready --json` output is valid JSON array
- `tq show --json` output is valid JSON object
- `tq update --claim` on already-claimed issue exits non-zero
- `tq dep add` cycle rejection exits non-zero with clear message
- `tq doctor --json` output schema
- Partial ID matching in commands

### What not to test

- No mocking. The store uses real files in temp directories.
- No testing of Go stdlib behavior (JSON marshaling, file I/O).
- No benchmarks. Performance is not a concern at orca's scale.

## Orca Integration Path

When orca's Go rewrite reaches the queue layer:

1. Add `tq` as a Go module dependency.
2. Orca's `internal/queue` package wraps `tq.Store`:
   - `queue.ReadReady()` calls `store.Ready()`
   - `queue.Claim()` calls `store.Update()` with claim opts
   - All operations happen against the primary repo's `.tq/` directory
3. Orca's lock-guarded write path becomes: acquire orca lock → switch to main → call tq store method → commit `.tq/` changes → push → release lock.
4. The `.tq` source-branch guard replaces the `.beads` guard: `merge-main.sh` rejects branches carrying `.tq/` changes.
5. Agent prompt changes `br ready --json` → `orca queue ready --json` (or equivalent subcommand).

The `tq` library knows nothing about git, branches, worktrees, or orca. The orca wrapper adds all of that.

### CLI subcommands in orca

For agents that need to call queue operations via CLI (before full library integration), orca can expose `orca queue <tq-command>` subcommands that delegate to the tq library with orca's locking and git semantics wrapped around them. This replaces the current `queue-write-main.sh` / `queue-read-main.sh` helpers.

## Design Decisions Log

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

### Step 1: Types and Validation
- `issue.go`: Issue, Comment, CreateOpts, UpdateOpts, ListFilter types
- `issue.go`: ID generation (prefix + 4 hex via crypto/rand)
- `issue.go`: validation functions (required fields, status values, priority range)
- `issue_test.go`: table-driven validation tests, ID format tests

### Step 2: Store — Init, Read, Write
- `store.go`: Store type, Init(), Open()
- `store.go`: internal readIssue(), writeIssue(), readAll(), resolveID() (partial matching)
- `store.go`: file locking (flock wrapper)
- `store_test.go`: init, duplicate init, read/write round-trip, partial ID resolution

### Step 3: Store — Operations
- `store.go`: Create(), Show(), List(), Update(), Close(), Reopen(), Comment()
- `store_test.go`: all operation tests (create+show round-trip, list filtering, claim semantics, close/reopen, comment append)

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
