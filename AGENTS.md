# Repository Guidelines

## Structure

The command entry point lives in `cmd/resticctl/`. Cobra wiring lives in
`internal/cli/`; workflows in `internal/app/`; profile loading and validation in
`internal/profile/`; subprocess integration in `internal/restic/`; and SQLite
snapshotting in `internal/sqlitebackup/`. Cron parsing lives in
`internal/cronexpr/`; scheduler integration in `internal/schedule/`; and
private last-run state and per-profile locking in `internal/runstatus/`. Keep
tests beside their owning package. Public configuration examples are embedded
resources under `internal/app/templates/`; real profiles and secrets must
remain outside the repository.

## Development

Use Go 1.25 or newer. Run the complete suite with:

```sh
go test ./...
```

Keep dependencies minimal; configuration uses the standard-library JSON package.
Use `filepath`, contexts, and argument-vector subprocess calls. Tests must use
temporary directories and fake runners; they must never contact a real Restic
repository or cloud service.

After every code change, deslop the changed code: remove unnecessary
complexity, duplication, dead code, noisy comments, and unrelated edits. Then
review the changed code for bugs, edge cases, security issues, and regressions;
fix any issues found before considering the change complete. Before creating a
commit containing code changes, run the relevant tests and `go test ./...` on
the development platform using Go 1.25 or newer. Fix any known test failures
before committing. Windows tests must execute on a Windows machine or runner;
cross-compiling with `GOOS=windows` alone is not sufficient. Run them locally
when a Windows runner is available; otherwise, report the missing check and
allow the commit, with Windows validation handled by CI. CI must pass on all
configured platforms before merging. Documentation-only changes do not require
Go tests.

Use Conventional Commits for commit messages (for example,
`feat(schedule): add profile schedule reconciliation`).

## Security

Never log or interpolate repository passwords into command lines. Keep credential
files private, validate all profile-derived names, and clean temporary plaintext
SQLite snapshots and password files on every normal or signaled exit.
