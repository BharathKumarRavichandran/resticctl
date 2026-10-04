# Principal Engineering Code Review

Review date: 2026-09-04

Remediation status checked: 2026-10-04

## Executive summary

The implementation is generally disciplined: it uses argument-vector subprocess
execution, strict profile decoding, private atomic writes, fake runners in tests,
and cross-platform CI. The original review identified seven P1 safety issues and
seven P2 reliability or maintainability issues.

## Remediation status

- **P1 findings 1–7: Fixed** in commit `d1bece6`
  (`fix(core): harden backup execution and scheduler safety`).
- **P2 findings 8–14: Fixed** in commit `c83dc60`
  (`fix(core): harden scheduling, monitoring, and atomic writes`).
- The P1 implementation passed the full test suite, race detector, vet, Windows
  cross-build, and Windows-specific test compilation.

The finding descriptions, recommended fixes, and line references below describe
the original review. The status notes describe the remediation; the original
descriptions are retained as historical context.

## P1: Fixed

### 1. Configured Restic dry runs are recorded as successful real backups

Status: Resolved. Loaded profiles now reject workflow-owned dry-run options,
and runtime classification recognizes `--dry-run`, `-n`, and explicit true
spellings before deciding whether to record a run.

Locations:

- `internal/app/service.go:133`
- `internal/app/execution.go:65`

`backup_args` and command-specific arguments are appended directly to the
Restic invocation. Status suppression, however, considers only the CLI's
`dryRun` boolean or an exact command-line `--dry-run` argument.

A profile containing any of the following can run under the scheduler, report
success, and create no snapshots:

```json
{
  "backup_args": ["--dry-run"]
}
```

Equivalent cases include `-n`, `--dry-run=true`, and a dry-run option supplied
through `commands.backup.args`.

Recommended fix:

- Reserve workflow-owned flags such as `--dry-run` and `-n` in configured
  arguments; or
- Classify the complete effective argument vector before deciding whether to
  record status.
- Add scheduled and direct-command tests for every supported dry-run spelling.

### 2. Valid cron expressions change meaning on systemd and launchd

Status: Resolved. Systemd and launchd installations now reject expressions
that restrict both day fields; the cron backend continues to support their
standard OR semantics.

Locations:

- `internal/schedule/native.go:38`
- `internal/schedule/schedule.go:366`
- `internal/cronexpr/cronexpr.go:12`

Standard cron treats restricted day-of-month and day-of-week fields with OR
semantics. For example, `0 0 1 * 1` means "the first of the month or Monday."
The generated systemd and launchd definitions combine both constraints, which
effectively requires both to match.

Catch-up continues to use the original cron parser, so normal scheduling and
catch-up can disagree about when a job is due.

Recommended fix:

- Reject expressions in which day-of-month and day-of-week are both
  restricted; or
- Render two native triggers that preserve cron's OR semantics.
- Add semantic equivalence tests across cron, systemd, and launchd.

### 3. Catch-up can execute twice after lock contention

Status: Resolved. Scheduled runs now acquire the profile lock, reload the last
successful run, and recompute due-ness before recording the start timestamp.

Locations:

- `internal/app/execution.go:82`
- `internal/app/execution.go:138`
- `internal/runstatus/status.go:75`

Due-ness is computed before acquiring the profile lock. With lock mode `wait`,
two simultaneous catch-up invocations can both decide they are due. The second
waits for the first to finish and then runs another backup without re-reading
the new success status.

The start timestamp is also captured before the lock wait, causing lock-wait
time to be reported as execution time.

Recommended fix:

- Acquire the lock before reading last-success state and deciding due-ness.
- Re-read status and recompute due-ness while holding the lock.
- Capture `StartedAt` only after lock acquisition.
- Add a deterministic concurrent catch-up test.

### 4. launchd network policy does not provide the promised gate

Status: Resolved. launchd installations reject the network requirement, and
generated plists no longer emit the obsolete `KeepAlive.NetworkState` key.

Location: `internal/schedule/launchd.go:119`

The launchd backend renders `KeepAlive.NetworkState`. Current macOS
documentation says `NetworkState` is no longer implemented. `KeepAlive` is also
a process-lifecycle/relaunch mechanism, not a one-shot availability
prerequisite.

Recommended fix:

- Implement network availability as a runtime preflight in the hidden
  scheduled-run workflow; or
- Reject `--require-network` for launchd.
- Do not model this policy using `KeepAlive`.

### 5. Credential privacy is not enforced on Windows

Status: Resolved. Windows credential validation requires an owner-only,
non-inheriting DACL. Newly created secret files and private state use the same
owner-only ACL policy.

Location: `internal/profile/platform_windows.go:17`

`ensureFileSecurity` is a no-op on Windows. Windows does not give Unix `0600`
mode bits equivalent ACL semantics, especially in a user-supplied configuration
directory.

This affects profile credentials, password files, internal state, and temporary
files containing secrets.

Recommended fix:

- Inspect and establish owner-only DACLs for secret-bearing files and
  directories.
- Reject insecure existing credential and password files.
- Add Windows-specific ACL tests.

### 6. Cancellation supervises processes, not process trees

Status: Resolved. Shared subprocess supervision now uses a dedicated Unix
process group or a Windows kill-on-close Job Object for Restic, hooks, password
commands, and database clients.

Locations:

- `internal/process/executor.go:52`
- `internal/restic/client.go:140`
- `internal/restic/password.go:21`

Restic, hooks, password commands, and database clients use plain
`exec.CommandContext`. Cancellation terminates the direct process, but spawned
descendants may survive.

This can leave work running after cancellation. On Windows, surviving children
may also keep plaintext staging files open and prevent their deletion.

Recommended fix:

- On Unix, run subprocesses in a dedicated process group and terminate the
  group on cancellation.
- On Windows, use a Job Object configured with kill-on-close semantics.
- Apply the same supervision contract to Restic, hooks, password commands, and
  database clients.
- Test a helper process that spawns a child and then receives cancellation.

### 7. Successful database clients are trusted without verifying artifacts

Status: Resolved. Every external provider verifies its expected artifact;
files must be regular and non-empty, and MongoDB output must contain a
non-empty regular dump file. PostgreSQL names beginning with `-` are rejected.

Locations:

- `internal/databasebackup/provider.go:50`
- `internal/databasebackup/provider.go:88`
- `internal/databasebackup/provider.go:116`

PostgreSQL, MongoDB, and MySQL providers return success as soon as the client
exits with status zero. They do not verify that the expected dump file or
directory exists and is suitable for backup.

A misconfigured executable, accidental help option, wrapper script, or client
regression can therefore produce a successful Restic snapshot containing no
database dump.

Recommended fix:

- Verify every expected artifact before invoking Restic.
- Require files to be regular and non-empty where that is a valid invariant.
- Require MongoDB's output directory and expected metadata/data files to exist.
- Reject PostgreSQL database names beginning with `-`, or pass the database
  through an unambiguous controlled option.

## P2: Fixed

### 8. Systemd jobs do not capture the installation PATH

Status: Resolved.

Locations:

- `internal/schedule/native.go:48`
- `internal/schedule/schedule.go:337`
- `internal/schedule/launchd.go:100`

Cron and launchd use the manager's captured `PATH`, but `renderSystemd` calls
`scheduledArguments` directly without adding an `Environment=PATH=...` entry.

Database-client preflight can pass during installation while the installed
timer later fails to locate Restic or a database client.

Recommended fix:

- Render the captured path into the systemd service with correct systemd
  escaping.
- Add a render test that checks `Environment=PATH=...`.

### 9. Scheduler rollback is not an exact rollback

Status: Resolved.

Locations:

- `internal/schedule/schedule.go:197`
- `internal/schedule/schedule.go:205`

Rollback reconstructs the previous job using the current executable and
environment. If either changed, the restored definition may not match the old
definition hash, leaving immediate drift after a failed update.

Recommended fix:

- Persist the complete desired schedule specification; or
- Capture and restore the exact previous installed definition.
- Test rollback after changing executable path, PATH, and backend.

### 10. Backend removal does not have consistent convergent semantics

Status: Resolved.

Locations:

- `internal/schedule/native.go:127`
- `internal/schedule/native.go:243`

Systemd silently ignores failure to disable a timer and may report successful
removal after leaving scheduler artifacts. Windows, conversely, treats an
already-missing task as fatal and can prevent cleanup of its state and XML
file.

Recommended fix:

- Treat scheduler "not found" responses as successful convergence.
- Attempt all cleanup steps and return joined material errors.
- Do not discard permission, disable, or unload failures.

### 11. Monitoring delivery is silent and serial

Status: Resolved.

Locations:

- `internal/app/execution.go:159`
- `internal/monitoring/monitoring.go:52`
- `internal/monitoring/monitoring.go:118`

Every reporting error is discarded by the application. Multiple unreachable
targets can add sequential per-target timeouts to every phase. HTTP reporting
also uses `context.WithoutCancel`, so cancellation does not stop these waits.

Non-fatal monitoring is the right policy, but non-fatal should not mean
undetectable.

Recommended fix:

- Emit secret-safe delivery diagnostics to stderr or a dedicated diagnostic
  sink.
- Apply one overall reporting budget per phase.
- Consider bounded parallel delivery for independent targets.
- Preserve a short cleanup budget after cancellation rather than removing
  cancellation without a global bound.

### 12. Temporary-file logging creates retained, undiscoverable files

Status: Resolved.

Location: `internal/monitoring/monitoring.go:314`

The `temporary-file` log destination creates a new file for each event, but its
path is never returned or reported and the file is never removed. A recorded
run can therefore leave several effectively unusable files in the temporary
directory.

Recommended fix:

- Remove this destination; or
- Create one lifecycle-managed file per run and expose its path; or
- Remove the file after delivery if it is intended only as transient storage.

### 13. Monitoring can report success when status finalization fails

Status: Resolved.

Location: `internal/app/execution.go:157`

The notification phase is selected from `runErr` alone. If Restic succeeds but
writing status/history or releasing the lock fails, the command returns an
error while external monitoring receives a succeeded status and `send-after`.

Recommended fix:

- Define whether status-finalization failure makes the overall action fail.
- Derive the final externally reported outcome from `errors.Join(runErr,
  finishErr)`.
- Keep the distinction between backup result and controller result as separate
  structured fields if operators need both.

### 14. Atomic state replacement is not fully durable across power loss

Status: Resolved.

Location: `internal/securefile/atomic.go:10`

The implementation syncs the temporary file before rename, but does not sync
the containing directory after rename. On filesystems requiring a directory
sync for rename durability, a sudden power loss can lose the new directory
entry.

Recommended fix:

- On supported Unix platforms, sync the parent directory after replacement.
- Document the weaker guarantee on platforms where an equivalent operation is
  unavailable.

## Maintainability remediation

The remaining cleanup items were addressed in the working tree on 2026-10-04:

- **Central action capabilities:** `internal/action` owns the managed action
  catalog, dry-run support, recording, scheduling, and prune support. CLI command
  generation, schedule reconciliation, group validation, monitoring configuration,
  and run-status validation use it. Execution handlers remain in the application
  layer, and monitoring continues to follow recorded actions.
- **Shared subprocess policies:** `internal/process` owns environment merging,
  platform-specific key normalization, and exit classification alongside existing
  process-tree supervision. Restic retains its argument construction and public
  exit-error type; password commands continue to use `internal/secretvalue`.
- **Simpler scheduler dispatch:** the forwarding backend interface and adapter
  types were removed. Explicit switches in `internal/schedule/backend.go`
  dispatch rendering and lifecycle operations to existing backend methods.
- **Shared private file lifecycle:** `internal/securefile` provides protected
  temporary-file creation, bounded temporary writes, exclusive file creation,
  and cleanup that preserves close and removal errors. Password files, MySQL
  options, profile creation, and atomic state writes use these helpers. Database
  selection manifests now use the same private atomic-write implementation.
  Callers retain responsibility for clearing in-memory secrets and deferring
  cleanup of successful temporary writes.

The follow-up review also corrected these edge cases:

- Environment merging preserves Windows drive-directory keys such as `=C:`.
- Subprocess classification and Restic exit conversion preserve joined supervision
  failures and cancellation while removing raw exit diagnostics.
- Restic warning policy suppresses only an allowed exit-code-3 warning; joined
  cleanup or cancellation failures still make the action fail.
- Temporary-file cleanup retains close failures, retries failed removals, and
  stops touching a path after successful removal.

Previously completed items:

- **Profile loading phases:** `profile.Load` calls explicit stages for credential
  binding, source normalization, argument validation, schedule normalization,
  monitoring validation, and runtime validation. Inheritance resolution lives in
  `internal/profile/inheritance.go`.
- **Deterministic database preflight errors:**
  `internal/databasebackup/preflight.go` sorts executable names and purposes
  before building operator-facing errors.

## Test status and remaining gaps

Added as part of the P1 remediation:

- Direct and concurrent tests of `app.ScheduledRun`.
- Effective dry-run tests covering CLI arguments, aliases, profile arguments,
  and configured command arguments.
- Native-backend rejection tests for cron day-field OR semantics.
- Subprocess-tree cancellation coverage.
- Zero-exit database-client tests with missing or invalid artifacts.
- Windows ACL tests for credential files.

Added as part of the P2 remediation:

1. Scheduler rollback with a changed backend, PATH, and executable.
2. Convergent and best-effort cleanup of missing or failed native scheduler jobs.
3. Parallel monitoring delivery and secret-safe delivery diagnostics.
4. Monitoring phase selection when status finalization fails.
5. Rejection of retained temporary monitoring logs.

Added as part of the maintainability cleanup:

- Temporary-file privacy before writing, idempotent cleanup, cleanup after close
  failure, and rejection of oversized data before creating a file.
- Exclusive creation preserves existing files and symlinks.
- Atomic replacement failure removes temporary data and preserves the destination.
- Shared environment merging preserves overrides, filtering, deterministic order,
  and platform-specific key semantics.
- Shared subprocess exit classification preserves exit codes without retaining
  diagnostic output, including wrapped and joined supervision failures.
- Warning policy preserves cleanup and cancellation failures joined with an
  allowed Restic warning.
- Repeated temporary cleanup preserves close errors, retries failed removal,
  and preserves a replacement file created after successful removal.
- Environment merging retains multiple Windows drive-directory entries.
- Scheduler backend/action integration tests iterate over the central catalog.
- The Windows ACL test creates its credential file through the temporary-file
  helper before validating its DACL.

The CI matrix in `.github/workflows/ci.yml` runs `go test ./...` on
`windows-latest`, including the Windows ACL and process-tree cancellation tests.
Runtime execution is configured; passing Windows CI results were not verified
during the 2026-10-04 status check.

## Verification performed during review

The 2026-10-04 maintainability cleanup passed the following checks on macOS:

- `go test ./...`.
- `go test -race ./...`.
- `go vet ./...`.
- Windows amd64 compilation of all packages and tests using
  `GOOS=windows GOARCH=amd64 go test -exec=/usr/bin/true ./...`; the Windows test
  binaries were compiled, not executed.
- Formatting and whitespace checks.

The results below are retained from the original review. Coverage was not
remeasured during the maintainability cleanup.

- `go test ./...`: passed.
- `go test -race ./...`: passed.
- `go vet ./...`: passed.
- Windows cross-build: passed.
- Windows-specific ACL and process-tree test compilation: passed.
- Statement coverage ranged from 17.1% to 81.2% by package.
- `internal/app` coverage: 57.0%.
- `internal/monitoring` coverage: 56.0%.

## Positive design choices to preserve

- Subprocess arguments are passed as vectors rather than through a shell.
- Repository/password override options are reserved.
- Profile and credential JSON decoding rejects unknown fields.
- Unix profile and credential ownership/mode checks are explicit.
- SQLite uses the online backup API and performs an integrity check.
- Database staging cleanup is deferred across normal workflow failures.
- Status does not persist raw command output or error strings.
- Monitoring events deliberately minimize secret-bearing fields.
- State files use private atomic replacement.
- Tests use temporary directories and fake runners.
- CI runs formatting, module consistency, vet, race detection, and tests across
  Linux, macOS, and Windows.
