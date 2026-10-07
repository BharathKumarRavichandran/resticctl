# Contributing to resticctl

Thank you for helping improve resticctl. Bug reports, documentation fixes, tests,
and focused code changes are welcome.

## Before you start

For substantial features or behavior changes, open an issue first so the scope
and user-facing design can be discussed before implementation. Report security
issues privately as described in [SECURITY.md](SECURITY.md).

## Development setup

Install Go 1.25 or newer. Restic is only required for manually running the
opt-in integration test; ordinary tests use fake runners and temporary files.

Run the standard checks from the repository root:

```sh
gofmt -w ./cmd ./internal
go mod tidy
go vet ./...
go test ./...
go test -race ./...
```

Set `RESTIC_INTEGRATION=1` to run the real Restic integration test. It creates a
temporary local repository and must never be pointed at an existing repository.

## Design guidelines

- Keep `cmd/resticctl` as a small composition root and Cobra-specific behavior
  in `internal/cli`.
- Put application workflows in `internal/app` and operating-system or
  subprocess integration behind narrow interfaces.
- Keep configuration loading and validation in `internal/profile`.
- Pass subprocess arguments as vectors without a shell. Never place repository
  passwords in arguments, errors, or logs.
- Use `filepath` for filesystem paths and propagate `context.Context` through
  blocking operations.
- Keep dependencies minimal and prefer the standard library when practical.
- Add tests beside the package that owns the behavior. Tests must use temporary
  directories and must not contact real repositories or cloud services.

Avoid adding a public `pkg` tree unless the project deliberately commits to a
stable library API. Internal package boundaries should follow responsibilities,
not mirror individual types.

## Pull requests

Keep changes focused and explain the user-visible behavior, important design
decisions, and validation performed. Update examples and tests with behavior
changes. Before submitting, remove unrelated edits and verify that formatting,
vetting, tests, and the race detector pass.
