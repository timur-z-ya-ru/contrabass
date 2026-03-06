# Local board tracker

Contrabass can now run against a filesystem-backed internal tracker with
`tracker.type: internal` (the legacy alias `local` is also accepted).

## Configuration

```yaml
tracker:
  type: internal
  board_dir: .contrabass/board
  issue_prefix: CB
```

## Storage layout

```text
.contrabass/
  board/
    manifest.json
    issues/
      CB-1.json
      CB-2.json
    comments/
      CB-1.jsonl
```

- `manifest.json` stores the issue prefix and next issue number.
- `issues/*.json` stores the board card source of truth.
- `comments/*.jsonl` stores append-only issue comments.

The internal board is intentionally separate from team runtime state under
`.contrabass/state/team/...`:

- `.contrabass/board/...` = long-lived tracker / kanban state
- `.contrabass/state/team/...` = execution-time worker coordination state

## CLI

```bash
contrabass board init --prefix CB
contrabass board create --title "Fix retry loop" --labels bug,orchestrator
contrabass board list
contrabass board move CB-1 in_progress
contrabass board comment CB-1 --body "agent run started"
contrabass board show CB-1
contrabass team run --config WORKFLOW.md --issue CB-1
```

Supported board states:

- `todo`
- `in_progress`
- `retry`
- `done`

## Orchestrator mapping

The internal board maps board states to the existing tracker contract:

- `todo` → `Unclaimed`
- `in_progress` → `Claimed` / `Running`
- `retry` → `RetryQueued`
- `done` → `Released`

This keeps the orchestrator logic unchanged while enabling local-first
project tracking.

## Team bridge

`contrabass team run --issue CB-1` hydrates the internal board issue into a
team run automatically.

Current bridge behavior:

- derives a default team name from the board issue when `--name` is omitted
- generates a plan → PRD → exec task chain from the board issue
- claims the board issue to the team automatically
- appends team lifecycle activity back to board comments
- moves the board issue to `done` on success or `retry` on run failure

This is the first slice of the autonomous board/team loop where AI agents can
create board issues, assign them to a team run, and let the runtime move the
ticket state automatically.
