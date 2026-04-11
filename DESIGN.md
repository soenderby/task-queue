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

### Field Mutability (v1)

| Field | Mutable? | How |
|---|---|---|
| `id` | no | immutable identity |
| `title` | no | set on create only |
| `description` | yes | `update --description` |
| `status` | constrained | `claim`, `close`, `reopen`, and restricted `update --status open` |
| `priority` | yes | `update --priority` |
| `type` | no | set on create only |
| `assignee` | yes | `update --assignee`, also set by `claim` / optional on `close` |
| `labels` | yes | `update --add-label` / `--remove-label` |
| `blocked_by` | yes (via dep ops) | `dep add` / `dep remove` only |
| `comments` | append-only | `comment` command only |
| `created_at` | no | set on create |
| `created_by` | no | set on create |
| `updated_at` | system-managed | set on each successful mutation |
| `closed_at` | system-managed | set/cleared by `close` / `reopen` |
| `close_reason` | system-managed + close input | set by `close --reason`, cleared by `reopen` |

### Statuses

Three statuses. No custom statuses, no state machine enforcement beyond these rules:

- `open` → can transition to `in_progress` (via `claim` only) or `closed` (via `close` only)
- `in_progress` → can transition to `open` (via `update --status open`) or `closed` (via `close`)
- `closed` → can transition to `open` (via `reopen` only)

The `claim` operation atomically sets `status=in_progress` and `assignee=<actor>`. It is the intended way for agents to pick up work, and it requires the issue to be ready under the same dependency rules as `ready`.

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
```

The CLI maps these to stderr messages and exit code 1. Error messages should be specific and actionable. Examples:

- `Claim` on an in_progress issue: `"cannot claim orca-a1b2: issue is not open (status: in_progress, assignee: agent-1)"`
- `Claim` on a blocked issue: `"cannot claim orca-a1b2: issue is blocked by orca-e5f6 (status: open)"`
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
└── .gitignore          # ignores runtime files (lock, temp)
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

`id_prefix` must match `^[a-z][a-z0-9-]{1,31}$` (2-32 chars, lowercase). `tq init` validates this and fails with `ErrInvalidPrefix` if invalid.

### .gitignore

Created by `tq init`:

```
lock
issues/*.tmp
```

The lock file and temp issue files are runtime-only and must not be committed.

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

- `--prefix`: ID prefix (default: lowercase basename of target directory)
- `--dir PATH`: target directory (default: CWD)
- Prefix validation uses `^[a-z][a-z0-9-]{1,31}$`.
- If the default basename-derived prefix is invalid, `init` fails with `ErrInvalidPrefix` and suggests passing `--prefix` explicitly.
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
- Supports partial ID prefix matching: `tq show a1b` matches `orca-a1b2` if unambiguous. Fails with error if multiple matches.

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

Preconditions:
1. Issue status must be `open`
2. Issue must be ready (every `blocked_by` ID must resolve to a valid, parseable issue with status `closed`)

If status is `in_progress` or `closed`, claim fails with `ErrAlreadyClaimed` (exit code 1) and a message identifying current status/assignee.
If status is `open` but dependencies are unresolved, claim fails with `ErrBlocked` and a message listing unresolved blockers.

This makes concurrent claims safe under file locking and prevents claiming blocked work.

- `--json` — output the claimed issue JSON.

#### `tq update ID [options]`

Update fields on an issue. For claiming work, use `tq claim` instead.

Options:
- `--status STATUS` — set status
- `--priority N` — set priority
- `--assignee NAME` — set assignee
- `--description TEXT` — set description (pass empty string to clear)
- `--add-label LABEL` — add a label (repeatable)
- `--remove-label LABEL` — remove a label (repeatable)
- `--json` — output updated issue JSON

All fields are independently optional. Only specified fields are changed. `updated_at` is set to now on every successful update.

Status changes via `update` are intentionally restricted:
- Allowed: `in_progress` → `open` (release back to open)
- Not allowed: `--status in_progress` (use `tq claim` so readiness checks are enforced)
- Not allowed: `--status closed` (use `tq close` so `closed_at` / `close_reason` semantics are explicit)
- Not allowed on closed issues (use `tq reopen`)

Disallowed forms fail with `ErrInvalidStatus`.

#### `tq close ID [--reason TEXT] [--actor NAME]`

Close an issue. Sets status to `closed`, `closed_at` to now. Optionally sets `close_reason` and `assignee`.

Precondition: issue must be `open` or `in_progress`. Closing an already-closed issue fails with `ErrInvalidStatus`.

#### `tq reopen ID`

Reopen a closed issue. Sets status to `open`, clears `closed_at` and `close_reason`.

Precondition: issue must be `closed`. Reopening an `open` or `in_progress` issue fails with `ErrInvalidStatus`.

#### `tq comment ID TEXT [--author NAME]`

Append a comment to an issue. Comments are append-only; there is no edit or delete.

#### `tq dep add ID BLOCKED_BY_ID`

Add a blocking dependency: ID is blocked by BLOCKED_BY_ID. Fails if:
- Either issue does not exist.
- ID == BLOCKED_BY_ID (self-dependency).
- The dependency would create a cycle.
- The dependency already exists.

#### `tq dep remove ID BLOCKED_BY_ID`

Remove a blocking dependency. Fails with `ErrNotFound` if either issue does not exist. Fails with `ErrDepNotFound` if both issues exist but the dependency edge is absent.

#### `tq dep list ID [--json]`

List dependencies for an issue. Shows what blocks it and what it blocks.

- Default: human-readable.
- `--json`: JSON object with `blocked_by` (issues that block this one) and `blocks` (issues this one blocks).

Implementation note: the reverse lookup ("what does this issue block?") requires reading all issues to find those whose `blocked_by` list contains this ID. Use `readAll()` for this operation — do not attempt indexing or caching at this scale.

#### `tq doctor [--json]`

Run health checks on the tq workspace.

Checks:

| ID | Check | Status on failure | Notes |
|---|---|---|---|
| `workspace_dir` | `.tq/` directory exists | fail | hard requirement |
| `config_valid` | `config.json` parses and validates | fail | includes `id_prefix` format |
| `issues_dir` | `issues/` directory exists | fail | hard requirement |
| `issue_json_valid` | Issue files parse as valid JSON | fail | malformed files are listed |
| `issue_filename_matches_id` | Issue file names match contained IDs | fail | rejects mismatches |
| `orphan_dependencies` | No orphan dependency references | fail | orphan blockers are treated as unresolved dependencies |
| `dependency_cycles` | No dependency cycles | fail | structural invariant |
| `stale_temp_files` | No stale `*.tmp` issue files | warn | indicates interrupted writes |

`DoctorReport.OK` is `true` only when there are zero `fail` checks. `warn` checks do not flip `OK` to false.

In JSON output, each check includes both a stable machine key (`id`) and a human label (`name`). Example:

```json
{"id":"orphan_dependencies","name":"No orphan dependency references","status":"fail","message":"orphan blocker IDs: orca-a1b2 -> orca-dead"}
```

### Output Conventions

- **Human output** (default): compact, tabular where appropriate. To stderr for errors, stdout for data.
- **JSON output** (`--json`): compact single-line JSON to stdout. `show` outputs one object. `list`, `ready`, `dep list` output arrays or structured objects. Errors and warnings are written to stderr only; stdout remains valid JSON.
- **Exit codes**: 0 on success. 1 on error. Errors always include a message on stderr.
- **Partial ID matching**: Any command that takes an ID accepts prefix matching only. Resolution order: exact full ID match first; otherwise, prefix match against full IDs and hex suffixes (e.g. `a1b` matches `orca-a1b2`). Ambiguous matches fail with an error listing candidates.

These output conventions are CLI-only. Library APIs do not print to stdout/stderr.

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

Partial ID matching is prefix-only: users can refer to `a1b2` or `orca-a1b`, and tq resolves it if unambiguous.

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
                is_ready = false             # missing/corrupt blocker: unresolved, blocks readiness
                break
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
- Orphan dependencies (blocked_by referencing a deleted/non-existent issue) are treated as unresolved and therefore blocking. `tq doctor` reports these as failures.
- Corrupt/malformed blocker files are also treated as unresolved and therefore blocking. `tq doctor` reports these as failures.
- Sort is deterministic: priority first (lower number = higher priority), then creation time (older first), then ID (lexicographic tiebreak).

## Cycle Detection

On `tq dep add A B` (A is blocked by B), detect whether adding this edge would create a cycle in the dependency graph.

Algorithm: starting from B, traverse `blocked_by` edges recursively (or BFS). If A is reachable from B, adding A→B would create a cycle. Reject with `ErrCycleDetected`.

This is O(n) in the number of issues, which is fine for orca's scale.

Cycle detection considers **all issues regardless of status**. A cycle like A (open) → B (closed) → C (open) → A is still rejected, even though B is closed and doesn't currently block anything. Rationale: closed issues can be reopened, and a structural cycle that becomes live on reopen is a latent hazard. The dependency graph is a structural invariant, not a runtime state.

Only `dep add` performs cycle detection. `doctor` also reports cycles as a diagnostic.

## Edge Cases

Explicit behavior for cases that implementing agents must not invent answers for:

**Closing an already-closed issue.** Fails with `ErrInvalidStatus`. Close is not idempotent.

**Closing an issue that blocks others.** Nothing cascades. The blocked issues are not modified. They simply become unblocked — their `blocked_by` list now points to a closed issue, which `ready` treats as resolved. No automatic status transitions, no notifications.

**Adding a dependency to or from a closed issue.** Allowed. The dependency is structural. A closed issue may be reopened later, and the dependency should be in place when it is. Cycle detection still applies regardless of issue status.

**Updating fields on a closed issue.** Allowed for mutable non-status fields. In v1 this means priority, labels, assignee, and description. Status changes on closed issues require `reopen`. `updated_at` is set to now.

**Setting `in_progress` via `update`.** Not allowed. `update --status in_progress` fails with `ErrInvalidStatus`; use `claim` so dependency readiness is enforced.

**Setting `closed` via `update`.** Not allowed. `update --status closed` fails with `ErrInvalidStatus`; use `close` so closure metadata is set intentionally.

**Malformed issue file.** If a file in `.tq/issues/` contains invalid JSON or fails ID validation, `list`, `ready`, and other multi-issue operations **skip the malformed file** rather than failing the whole command. `doctor` reports malformed files as errors. `show` on the specific malformed issue returns `ErrCorruptFile`. All single-target mutations (`claim`, `update`, `close`, `reopen`, `comment`, `dep add`, `dep remove`) on a corrupt target issue return `ErrCorruptFile`.

CLI behavior: prints warnings for skipped files to stderr.
Library behavior: does not write to stderr/stdout; it returns best-effort results and leaves diagnostics to `Doctor()` / caller policy.

**Empty issues directory.** `ready`, `list`, and `dep list` return empty results, not errors.

**Orphan dependency reference.** If an issue references a non-existent blocker ID, that blocker is treated as unresolved: the issue is not ready and cannot be claimed. `doctor` reports this as a failure.

**Corrupt blocker issue file.** If a blocker issue file exists but is malformed/corrupt, it is treated as unresolved for `ready` and `claim` (same effect as orphan blocker). `doctor` reports the corrupt file as a failure.

**Reopening an issue.** Sets status to `open`. Clears `closed_at` and `close_reason`. Does **not** clear `assignee` — the previous assignee may want to continue the work. To clear the assignee, use `tq update ID --assignee ""`.

**Reopening a non-closed issue.** Fails with `ErrInvalidStatus`. Reopen only works on `closed` issues.

**Claiming a blocked issue.** Not allowed. If status is `open` but at least one blocker is not `closed`, `claim` fails with `ErrBlocked` and reports unresolved blockers.

**Claiming an already-claimed issue.** Fails with `ErrAlreadyClaimed`. The error message includes the current status and assignee so the caller knows who holds it. The caller must pick a different issue or wait.

**Duplicate dependency.** `dep add A B` when A already has B in `blocked_by` fails with `ErrDupDep`. This is not silent — the caller should know the state didn't change.

**Removing a non-existent dependency.** `dep remove A B` when either issue does not exist fails with `ErrNotFound`. When both exist but the edge is absent, fails with `ErrDepNotFound`.

**Stale temporary files (`*.tmp`).** If interrupted writes leave `.tq/issues/*.tmp` files behind, normal commands ignore them. `tq doctor` reports them as warnings.

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

### Platform assumptions

- v1 targets local POSIX-like filesystems (Linux/macOS/WSL).
- Locking assumes `flock` semantics for `.tq/lock`.
- Atomic write safety assumes rename-in-place semantics on the same filesystem.
- Network/distributed filesystems with weak locking/rename guarantees are out of scope for v1.

### Implementation Notes

These resolve small but real decisions the implementer would otherwise make ad-hoc. They are binding for v1.

**1. Priority default handling.**
`CreateOpts.Priority` is `*int`. `nil` means "use default 2". Non-nil values are validated against 0–4. Zero is a valid value (critical), not a sentinel. The CLI `-p/--priority` flag passes through as a `*int` (unset flag → `nil`).

**2. Empty-string clearing on Update.**
For pointer-typed string fields in `UpdateOpts` (`Description`, `Assignee`), a non-nil pointer to an empty string clears the field. A nil pointer means "do not change". This applies uniformly. CLI passes `--description ""` and `--assignee ""` as non-nil empty strings.

**3. File locking.**
Use `syscall.Flock(fd, syscall.LOCK_EX)` on `.tq/lock`. Blocking acquisition, no timeout in v1. The lock file is created if missing, opened `O_RDWR|O_CREATE` with mode `0o644`. The fd is closed after the mutation completes, which releases the lock. Rationale: at tq's scale and usage pattern (single writer, usually under an outer orca lock), contention is effectively zero. Adding a timeout is deferred until a concrete failure mode appears.

**4. Durable atomic writes.**
Write sequence for issue files:
1. Create `.tq/issues/{id}.json.tmp` with `O_WRONLY|O_CREATE|O_TRUNC`, mode `0o644`
2. Write JSON payload
3. `fsync` the tmp file
4. `rename` tmp → final path
5. `fsync` the `.tq/issues/` directory

Step 5 ensures the rename is durable across crashes on ext4/xfs. Steps 3 and 5 are required for v1 crash-safety claims.

**5. JSON serialization details.**
- Indent: 2 spaces via `json.MarshalIndent(v, "", "  ")`
- Trailing newline: yes (one `\n` appended after the marshaled bytes) — keeps files POSIX-text and git-friendly
- Empty slices: serialize as missing fields (all slice fields on `Issue` use `omitempty`). Do not emit `"labels": []` or `"blocked_by": []`. Rationale: smaller files, cleaner diffs, fewer no-op churn lines.
- Field order: follows the Go struct declaration order in `issue.go` (which is the canonical order used in the design doc example).
- Unknown fields on read: `json.Decoder.DisallowUnknownFields()` is **not** used. Forward-compatible reads; unknown fields are ignored. Round-tripping is not guaranteed to preserve unknown fields.

**6. Time source.**
`Store` holds an internal `now func() time.Time`, defaulting to `time.Now`. It is not part of the public `Open`/`Init` signature. Tests inject fake clocks via an unexported helper (e.g. `newStoreForTest(dir string, now func() time.Time)`). All timestamps written by mutations use `s.now().UTC().Format(time.RFC3339)` for consistent UTC RFC 3339 output.

**7. Module path.**
`github.com/soenderby/task-queue`. Package name `tq`. Binary name `tq` built from `./cmd/tq/`.

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
func (s *Store) DepList(id string) (*DepGraph, error)

// Diagnostics
func (s *Store) Doctor() (*DoctorReport, error)

// All reads and writes in these methods handle file locking internally.
```

Library note: public API methods never write to stdout/stderr. Callers own presentation and warning policy.

### Option Types

```go
type CreateOpts struct {
    Title       string
    Description string
    Priority    *int     // nil = default 2; 0-4 otherwise
    Type        string   // default "task"
    Labels      []string
    Assignee    string
    CreatedBy   string
}

type UpdateOpts struct {
    Status       *string  // nil = don't change
    Priority     *int
    Assignee     *string
    Description  *string
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
    ID      string `json:"id"`      // stable machine key, e.g. "orphan_dependencies"
    Name    string `json:"name"`    // human-readable label
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
  - Orphan dependency (non-existent blocker) → not ready
  - Corrupt/malformed blocker file → not ready
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
- **List with malformed file**: skips corrupt file and returns valid issues
- **Update**: changes fields (including description), updates `updated_at`
- **Update description clear**: `--description ""` clears description
- **Update closed issue**: can change priority, labels, assignee, description
- **Update status on closed issue**: fails with `ErrInvalidStatus`
- **Update --status in_progress**: fails with `ErrInvalidStatus` (must use `Claim`)
- **Update --status closed**: fails with `ErrInvalidStatus` (must use `Close`)
- **Claim**: sets status + assignee for ready open issues
- **Claim already in_progress**: fails with ErrAlreadyClaimed
- **Claim blocked open issue**: fails with `ErrBlocked` and unresolved blocker details
- **Claim with orphan blocker**: fails with `ErrBlocked`
- **Claim with corrupt blocker file**: fails with `ErrBlocked`
- **Close**: sets status, closed_at, close_reason
- **Close with actor**: sets assignee
- **Close already closed**: fails with `ErrInvalidStatus`
- **Reopen**: clears closed_at, close_reason, sets status to open, preserves assignee
- **Reopen non-closed issue**: fails with `ErrInvalidStatus`
- **Comment**: appends to comments list
- **Mutation on corrupt target issue**: returns `ErrCorruptFile` (claim, update, close, reopen, comment)
- **Dep add**: adds to blocked_by, fails on cycle, fails on self, fails on non-existent, fails on duplicate (`ErrDupDep`)
- **Dep add to/from closed issue**: succeeds (deps are structural)
- **Dep remove**: removes from blocked_by
- **Dep remove non-existent edge**: fails with `ErrDepNotFound`
- **Dep remove non-existent issue**: fails with `ErrNotFound`
- **Dep list**: returns `blocked_by` and reverse `blocks` views
- **Ready**: end-to-end with real files
- **Ready with orphan blocker**: excludes issue from ready results
- **Ready with malformed file**: skips corrupt file, returns valid ready issues
- **Ready with malformed blocker reference**: affected issue is not ready
- **Doctor**: detects orphan deps, cycles, invalid files, malformed JSON, stale `*.tmp` files

### CLI tests

Build the binary, run it against a temp directory, check exit codes and stdout/stderr. These test the command parsing and output formatting layer — they should not duplicate the store test logic.

- `tq init` + `tq create` + `tq show` round-trip
- `tq init --dir PATH` creates workspace at PATH
- `tq ready --json` output is valid JSON array
- `tq show --json` output is valid JSON object
- `tq claim` on already-claimed issue exits non-zero with status and assignee in message
- `tq claim` on blocked open issue exits non-zero with blocker details
- `tq claim` with orphan blocker exits non-zero with blocker details
- `tq claim` with corrupt blocker exits non-zero with blocker details
- `tq claim` on ready open issue succeeds
- `tq update --status in_progress` exits non-zero (must use `claim`)
- `tq update --status closed` exits non-zero (must use `close`)
- `tq close` on already-closed issue exits non-zero
- `tq reopen` on non-closed issue exits non-zero
- `tq dep add` cycle rejection exits non-zero with clear message
- `tq dep list --json` output has `blocked_by` and `blocks`
- malformed file warning behavior: warning on stderr while stdout remains valid JSON
- `tq doctor --json` output schema (including per-check `id` and `name`)
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
Claim has unique preconditions (status must be `open` and dependencies must be ready), unique side effects (sets both status and assignee atomically), and is the single most important operation for agent coordination. Bundling it into a general-purpose Update struct creates ambiguity about precedence when Claim and Status are both set, and forces implementers to handle interaction rules that don't need to exist. A separate `Claim(id, actor)` method is simpler, more predictable, and independently testable.

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

## Tradeoffs (Explicit, Accepted)

These are the main costs accepted by the current design. They are intentional and not treated as defects.

| Decision | Benefit | Accepted Cost |
|---|---|---|
| Per-issue JSON files under `.tq/issues/` | Great git diffs, simple direct inspection, atomic per-issue writes | Directory scan for many operations; no rich querying/indexing |
| No database | Zero dependencies, easy portability | No secondary indexes, no SQL-style queries, linear scans |
| No daemon/service | Simpler ops model, no background state | Every command pays startup/read cost; no push-based notifications |
| tq does not run git | Clean separation of concerns; reusable outside git | Callers must implement commit/push discipline correctly |
| Three statuses only (`open`, `in_progress`, `closed`) | Low cognitive load and transition complexity | Less expressive lifecycle; some states must be inferred |
| Single dependency type (`blocked_by`) | Minimal model; readiness stays simple | Cannot represent richer relationships structurally |
| `ready` includes only `open` issues | Clean “what can be picked now” semantics | Separate reporting required for `in_progress` work |
| `claim` requires readiness (not just `open` status) | Prevents agents from starting blocked work and reduces queue confusion | No early reservation of blocked issues; handoff patterns must use comments/assignee updates instead |
| Orphan blockers treated as blocking in `ready`/`claim` | Prevents work from starting when dependency graph is inconsistent | Stale/mistyped dependency IDs can halt progress until fixed |
| Prefix-only partial ID matching | Deterministic and predictable resolution | Less flexible than fuzzy/substring matching |
| Read operations are unlocked | Low contention and simple implementation | Reads are not transactional snapshots |
| POSIX-local filesystem assumptions (`flock`, atomic rename) | Simple and robust on target environments | Native Windows / weak network FS semantics are out of v1 scope |
| Description is mutable via `update` | Allows refinement as understanding improves | Potential drift/churn in issue text; original wording is not preserved unless tracked in comments/git history |

## Implementation Sequence

Build in this order. Each step produces a `go test ./...`-passing commit.

### Step 1: Types, Errors, and Validation
- `issue.go`: Issue, Comment, CreateOpts, UpdateOpts, ListFilter types
- `issue.go`: sentinel errors (ErrNotFound, ErrAlreadyClaimed, ErrBlocked, ErrCycleDetected, etc.)
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
- `store_test.go`: all operation tests (create+show round-trip, list filtering, claim semantics including `ErrAlreadyClaimed` and `ErrBlocked`, update on open and closed issues, close/reopen, comment append)

### Step 4: Ready and Cycle Detection
- `ready.go`: Ready() computation, hasCycle() detection
- `store.go`: DepAdd(), DepRemove(), DepList() (using cycle detection)
- `ready_test.go`: table-driven ready computation and cycle detection tests
- `store_test.go`: dep add/remove/list tests with real files

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

## Decisions Required Before Implementation

None. This design is implementation-ready as written.

## V1 Scope Decisions

The following are explicitly decided for v1:

1. **`ready` does not include `in_progress` issues.**
   - `ready` returns only issues with `status == open` whose blockers are closed.
   - No `--include-in-progress` flag in v1.

2. **Bulk operations are out of scope for v1.**
   - No commands like `tq close --all-ready`.
   - Callers can loop over individual operations.

3. **Archive/purge is out of scope for v1.**
   - Closed issues remain in `.tq/issues/`.
   - No `tq archive` command in v1.
