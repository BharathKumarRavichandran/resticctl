# TODO

Roadmap for adding useful `resticprofile` capabilities while preserving
resticctl's existing profile format, secure credential handling, argument-vector
execution, and native database-backup workflows. This is a capability-parity
roadmap, not a requirement to copy resticprofile's configuration syntax or
shell-execution behavior exactly.

## Phase 1: Hooks

- [x] Add optional `run-before` hooks.
- [x] Add optional `run-after` hooks for successful backups.
- [x] Add optional `run-after-fail` hooks.
- [x] Add optional `run-finally` hooks that run on both success and failure.
- [x] Execute hooks with contexts, timeouts, and argument vectors; do not invoke
  a shell implicitly.
- [x] Define hook failure semantics and document whether failures stop the
  backup or are reported only.
- [x] Ensure SQLite snapshot and temporary credential cleanup still happens
  when hooks fail or are interrupted.
- [x] Add tests using fake runners and temporary directories.

## Phase 2: Backup orchestration

- [x] Add optional integrity check before backup (`check-before`).
- [x] Add optional integrity check after backup (`check-after`).
- [x] Add retention/prune before backup.
- [x] Add retention/prune after backup.
- [x] Make orchestration order explicit and preserve SQLite staging semantics:
  create a consistent temporary copy, back up that copy, then clean it up.
- [x] Add configuration and CLI documentation for orchestration options.
- [x] Add tests for success, failure, and cleanup paths.

## Phase 3: Inheritance and shared defaults

- [x] Add an optional parent profile or defaults section.
- [x] Define merge rules for scalar values, lists, restic arguments, tags,
  hooks, and SQLite databases.
- [x] Detect inheritance cycles and invalid or missing parents.
- [x] Keep credentials out of inherited/shared public configuration where
  possible.
- [x] Validate the fully resolved profile before execution.
- [x] Add tests for overrides, nested inheritance, cycles, and validation.

## Phase 4: Multiple profiles per configuration file

- [ ] Design an optional multi-profile JSON format without breaking existing
  one-profile-per-file configurations.
- [ ] Support selecting a profile by name from the CLI.
- [ ] Validate profile names and reject duplicate names.
- [ ] Define how credentials, defaults, and inheritance are scoped.
- [ ] Add profile descriptions and display them in profile listings.
- [ ] Add named groups that run multiple profiles sequentially.
- [ ] Define group failure behavior, including an explicit continue-on-error
  option.
- [ ] Support running every configured profile without constructing a group.
- [ ] Detect missing profiles, cycles, and duplicate membership in groups.
- [ ] Update profile discovery and user-facing documentation.
- [ ] Add migration, inheritance, group-execution, and compatibility tests.

## Phase 5: Scheduling foundations and status

- [x] Add a `schedule` command that can install, inspect, and remove backup
  schedules.
- [x] Support cron schedules on Unix-like systems.
- [x] Support macOS `launchd` via generated per-profile `.plist` files.
- [x] Install launchd jobs persistently in the user's `Library/LaunchAgents`.
- [x] Allow schedules to be declared in profile configuration.
- [x] Run one overdue backup after boot/login/wake when catch-up is enabled.
- [x] Support independent scheduled `forget` retention jobs and optional prune.
- [x] Support hourly, daily, weekly, monthly, and yearly schedule aliases.
- [x] Add a platform abstraction so additional schedulers can be supported
  later (for example, systemd timers or Windows Task Scheduler).
- [x] Use stable, validated job names derived from profile names.
- [x] Make generated jobs invoke an absolute `resticctl` path with an explicit
  configuration directory and selected profile.
- [x] Provide install, uninstall, list/status, and dry-run operations.
- [x] Avoid placing repository passwords or other secrets in generated jobs,
  logs, or command-line arguments.
- [x] Define locking/concurrency behavior so overlapping backups do not corrupt
  SQLite staging or run duplicate work.
- [x] Record last-run status, start/end time, duration, and exit result without
  storing secrets.
- [x] Add status output suitable for scripts and monitoring systems.
- [x] Add tests for cron and launchd rendering without installing real jobs.
- [x] Record status independently for backup, check, forget, prune, and copy.
- [x] Reconcile all schedules declared by a profile in one operation.
- [x] Reconcile schedules for all profiles with an explicit `--all` operation.

## Phase 6: More direct Restic flag support

- [x] Expose additional restic command options without requiring a new wrapper
  for every flag.
- [x] Preserve argument-vector execution and reject unsafe or ambiguous values.
- [x] Define precedence between profile options, command-line options, and
  orchestration defaults.
- [x] Document supported restic commands and pass-through behavior.
- [x] Add pass-through support for currently omitted Restic commands, including
  `mount` and `rewrite`.
- [x] Track Restic command additions without silently accepting misspelled
  commands or weakening reserved credential-option checks.
- [x] Add first-class, persistent command sections for `backup`, `cache`, `cat`,
  `check`, `copy`, `diff`, `dump`, `features`, `find`, `forget`, `init`, `key`,
  `key add`, `key list`, `key passwd`, `key remove`, `list`, `ls`, `migrate`,
  `mount`, `prune`, `rebuild-index`, `recover`, `repair index`, `repair packs`,
  `repair snapshots`, `restore`, `rewrite`, `snapshots`, `stats`, `tag`, and
  `unlock`.
- [x] Allow shared profile-level Restic flags to flow only into commands that
  support them.
- [x] Preserve raw argument lists as an escape hatch for new Restic flags.
- [x] Generate command help from the installed Restic version where practical.
- [x] Add argument-construction tests for every newly supported option.

## Phase 7: Additional database backends

- [x] Define a database-backup provider interface.
- [x] Keep SQLite on the native online-backup API.
- [x] Add PostgreSQL support using `pg_dump`.
- [x] Optionally support PostgreSQL globals using `pg_dumpall --globals-only`.
- [x] Add MongoDB support using `mongodump`.
- [x] Document consistency requirements and limitations for MongoDB replica
  sets and deployments.
- [x] Add MySQL and MariaDB support using `mysqldump` or `mariadb-dump`.
- [x] Support MySQL/MariaDB connections over localhost, remote TCP endpoints,
  and Unix sockets.
- [x] Deliver MySQL/MariaDB credentials through a private temporary client
  option file; never place passwords in process arguments, inherited
  environment variables, schedules, logs, or status files.
- [x] Define consistent MySQL/MariaDB dump behavior, including
  `--single-transaction` for transactional tables and documented locking
  requirements for non-transactional tables.
- [x] Allow optional routines, events, triggers, and database-object selection
  while reserving arguments that could override credentials or dump output.
- [x] Stage each logical dump under a stable `databases/<name>.sql` snapshot
  path and remove both dumps and temporary client option files on every exit.
- [x] Validate the selected dump client and its configured arguments without
  connecting to a database.
- [x] Add MySQL/MariaDB restore guidance using the matching client tools.
- [x] Add fake-runner tests for local, socket, and remote MySQL/MariaDB
  configurations, credential isolation, failures, cancellation, and cleanup.
- [x] Support databases hosted locally or on a remote database server.
- [x] Run database client tools on the machine executing `resticctl`.
- [x] Stage PostgreSQL and MongoDB dumps in a temporary directory on the
  `resticctl` host, back up the staged files, and remove them in `run-finally`.
- [x] Support Unix sockets, localhost, and remote TCP/database endpoints as
  appropriate for each backend.
- [x] Keep database credentials in private credential files or environment
  variables; never put them in command-line arguments, schedules, or logs.
- [x] Allow configuring client executable paths and safe backend-specific
  arguments.
- [x] Add restore guidance and matching restore commands for every backend.
- [x] Add fake-runner tests for local and remote configurations without
  contacting real database servers.

## Phase 8: Configuration formats and composition

- [ ] Add YAML configuration support.
- [ ] Add TOML configuration support.
- [ ] Add HCL configuration support if it can be implemented without an
  excessive dependency or maintenance burden.
- [ ] Keep JSON and the current one-profile-per-file layout fully compatible.
- [ ] Add configuration includes with deterministic merge rules and cycle
  detection.
- [ ] Add safe configuration templating with documented variables and helper
  functions.
- [ ] Ensure template evaluation cannot expose credential values or execute
  arbitrary commands implicitly.
- [x] Add a trace/show command that renders the resolved, inherited profile with
  secrets redacted.
- [ ] Add profile working-directory (`base-dir`) support.
- [ ] Add dotenv/environment-file loading with private-file validation and
  explicit precedence over inherited process variables.
- [ ] Add global defaults for configuration-wide behavior without allowing
  credentials to leak across profiles.
- [ ] Add tests for every format, include merge, template expansion, redaction,
  path resolution, and compatibility combination.

## Phase 9: Profile groups and group schedules

- [ ] Allow every supported profile action to run against a named group.
- [ ] Preserve deterministic profile ordering within a group.
- [ ] Report each member's result and an aggregate group result.
- [ ] Support group-level continue-on-error policy.
- [ ] Install, inspect, reconcile, and remove schedules at group scope.
- [ ] Schedule group backup, check, forget, prune, and copy actions.
- [ ] Prevent group and member schedules from causing unsafe overlapping runs.
- [ ] Record group status without replacing individual profile status.
- [ ] Add tests for partial failure, cancellation, locking, status, and scheduled
  group execution.

## Phase 10: Complete scheduler coverage

- [x] Add native systemd user and system timer backends.
- [x] Add Windows Task Scheduler support.
- [x] Support explicit crontab files, including system crontabs with a user
  column, where appropriate.
- [x] Schedule check, prune, and copy independently in addition to backup and
  forget.
- [x] Support multiple calendar expressions for one scheduled action.
- [x] Define portable calendar syntax and document backend-specific limitations.
- [x] Add user, logged-on-user, and system permission modes.
- [x] Add scheduler process-priority/background modes.
- [x] Add per-schedule log destinations.
- [x] Add schedule lock modes and bounded lock waiting.
- [x] Add scheduler start/enable controls and a no-start installation option.
- [x] Add conditions for network availability and AC power where supported.
- [x] Add a true schedule dry run that renders changes without installing them.
- [x] Add cross-platform rendering and fake-installation tests for every
  scheduler and scheduled action.

## Phase 11: Monitoring, notifications, and logs

- [x] Extend status records with command name, exit code, error category, Restic
  warning state, and optional redacted backup statistics.
- [x] Preserve a history or per-command view instead of only the latest
  backup/forget result.
- [x] Add configurable JSON status-file export for external monitoring tools.
- [x] Add Prometheus textfile export for backup status and statistics.
- [x] Add Prometheus Pushgateway delivery with configurable job names and
  user-defined labels.
- [x] Add HTTP `send-before`, `send-after`, `send-after-fail`, and `send-finally`
  hooks for backup, check, forget, prune, and copy.
- [x] Support multiple HTTP targets, methods, headers, bodies, body templates,
  timeouts, and custom CA certificates.
- [x] Define non-fatal delivery semantics so monitoring failures do not mask the
  backup result.
- [x] Add background handlers for non-fatal Restic warnings.
- [x] Add an explicit policy for treating Restic warning exit codes as success,
  warning, or failure.
- [x] Add console, local-file, temporary-file, local-syslog, and remote-syslog
  logging destinations where supported.
- [x] Redact repository credentials, database credentials, sensitive headers,
  paths selected by policy, and temporary password-file names from monitoring
  and logs.
- [x] Add fake HTTP, Pushgateway, and syslog tests; tests must not contact an
  external service.

## Phase 12: Runtime policies and host safety

- [x] Add portable process-priority controls and Unix `nice` support.
- [x] Add Linux `ionice` class and level controls.
- [x] Add Windows process priority-class support.
- [x] Add a minimum-available-memory preflight check.
- [x] Add an option to prevent idle sleep while a job is running.
- [x] Add battery/AC-power policy checks before scheduled jobs.
- [x] Add network-online policy checks before remote-repository jobs.
- [x] Add configurable local lock files, bounded lock waits, and explicit
  fail/ignore modes.
- [x] Detect stale local locks and require an explicit policy before clearing
  them.
- [x] Add safe automatic recovery for stale Restic repository locks, with dry
  run and age/activity checks.
- [x] Ensure cancellation and termination always restore host state and release
  locks.
- [x] Add platform-specific tests using fake host probes and process runners.

## Phase 13: Streaming and automatic initialization

- [x] Support backing up a stream from standard input with a required logical
  filename.
- [x] Support an argument-vector producer command whose stdout is streamed to
  Restic without an implicit shell or plaintext staging file.
- [x] Define how streaming backups interact with normal paths, database staging,
  dry runs, hooks, cancellation, and status reporting.
- [x] Allow a profile to initialize its repository automatically when it is
  confirmed missing.
- [x] Prevent automatic initialization on ambiguous network, authentication, or
  permission failures.
- [x] Make automatic initialization opt-in and safe under concurrent runs.
- [x] Add fake-runner tests for large streams, producer failure, cancellation,
  repository probing, and initialization races.

## Phase 14: Secondary repositories and copy workflows

- [ ] Add a declarative `copy` target with an independently configured
  destination repository and credential source.
- [ ] Keep source and destination credentials isolated and out of command-line
  arguments, generated schedules, logs, and status files.
- [ ] Allow copy filters and snapshot selectors to be stored in the profile.
- [ ] Optionally initialize a missing destination repository.
- [ ] Support initialization with `copy-chunker-params` from the source
  repository.
- [ ] Add copy-specific hooks, HTTP notifications, locking, status, and
  schedules.
- [ ] Validate source/destination combinations and refuse accidental copying to
  the same repository where it can be detected.
- [ ] Add fake two-repository tests without contacting a real repository.

## Phase 15: User and developer tooling

- [ ] Generate a cryptographically secure Restic password/key file with private
  permissions and safe overwrite behavior.
- [ ] Generate JSON Schema documents for every supported configuration version.
- [ ] Publish versioned schemas suitable for JSON, YAML, and TOML editor
  validation where supported.
- [ ] Generate a complete configuration reference from the profile model.
- [x] Add `show` output for resolved profiles with credentials and sensitive
  values redacted. Extend it to groups when groups are implemented.
- [ ] Add integrated help for both resticctl commands and the installed Restic
  command/flags.
- [ ] Keep Bash, Zsh, Fish, and PowerShell completion generation tested.
- [ ] Add configuration upgrade tooling for deprecated fields and future schema
  versions.
- [ ] Add documentation that maps resticprofile concepts to their resticctl
  equivalents and identifies deliberate security differences.

## Compatibility and quality

- [ ] Preserve the current one-profile-per-JSON-file format as the default.
- [ ] Keep native SQLite online backup and integrity checking enabled for
  configured SQLite databases.
- [ ] Keep credentials in private files or direct password commands.
- [ ] Keep hooks and producer commands as explicit argument vectors; do not add
  implicit shell evaluation for parity.
- [ ] Clean temporary SQLite snapshots and password files on normal,
  signaled, and failure exits where possible.
- [ ] Clean PostgreSQL and MongoDB staging on normal, signaled, and failure exits.
- [ ] Treat configuration rendering, status, monitoring, logging, and scheduler
  generation as secret-bearing surfaces and test their redaction.
- [ ] Update README examples and embedded configuration templates as features
  are implemented.
- [ ] Run `go test ./...` for each completed phase.
