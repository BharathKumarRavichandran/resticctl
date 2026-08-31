# resticctl

A profile-based command-line wrapper around [restic](https://restic.net/) for backups.

A profile keeps the repository, paths, credentials, and retention rules in one place.
The package also supports consistent backups of live SQLite databases, adding
the copies to the same snapshot as the rest of the files.

Restic handles the backup, encryption, restore, and repository maintenance.

## Requirements

- restic
- Go 1.25 or newer to build from source

Build it from the repository root:

```sh
go build -o resticctl ./cmd/resticctl
```

Install the binary in a directory on your `PATH`, for example:

```sh
# Linux or macOS (user-local)
install -m 0755 resticctl ~/.local/bin/resticctl

# macOS or Linux (system-wide, if /usr/local/bin is on your PATH)
sudo install -m 0755 resticctl /usr/local/bin/resticctl
```

On Windows, place `resticctl.exe` in a directory listed in `PATH`.

## Setup

Create a profile:

```sh
resticctl create <profile>
```

Replace `<profile>` with a name that describes the backup.

This creates `<profile>.json` and `<profile>.credentials.json`. On Linux and macOS they
live in `~/.config/resticctl`; on Windows they live in
`%APPDATA%\resticctl`.

Edit both files, then initialize the repository:

```sh
resticctl init <profile>
```

Preview and run a backup:

```sh
resticctl backup <profile> --dry-run
resticctl backup <profile>
```

The config directory can be changed with `--config-dir` or
`RESTICCTL_CONFIG_DIR`.

## Profile format

Profiles are JSON. Relative paths are resolved from the profile directory. `~`,
`$VAR`, and `${VAR}` are expanded; an unset variable is treated as an error.

```json
{
  "repository": "s3:https://s3.us-west-004.backblazeb2.com/my-bucket/restic",
  "credentials_file": "<profile>.credentials.json",
  "restic_args": ["--retry-lock", "2m"],
  "backup_paths": ["~/Documents", "~/Pictures"],
  "backup_args": ["--exclude-caches", "--skip-if-unchanged"],
  "tags": ["personal"],
  "forget_args": [
    "--keep-daily", "7",
    "--keep-weekly", "5",
    "--keep-monthly", "12"
  ],
  "forget": {
    "cron": "@weekly",
    "backend": "auto",
    "catch_up": true,
    "prune": false
  },
  "check_args": [],
  "check_before": true,
  "check_after": false,
  "prune_before": false,
  "prune_after": true,
  "run_before": [
    {"command": ["/usr/local/bin/prepare-backup"], "timeout": "30s"}
  ],
  "run_after": [],
  "run_after_fail": [],
  "run_finally": [
    {"command": ["/usr/local/bin/finish-backup"], "timeout": "30s"}
  ],
  "schedule": {
    "backend": "auto",
    "cron": "0 2 * * *",
    "catch_up": true
  },
  "sqlite_databases": [
    {
      "name": "notes",
      "path": "~/Library/Application Support/notes/data.sqlite3"
    }
  ]
}
```

`restic_args` are placed before the restic subcommand. The other argument lists
only apply to the command named by the field.

`backup_paths` contains ordinary files and directories to include. SQLite
databases are listed separately in `sqlite_databases`; for a SQLite-only
profile, set `backup_paths` to `[]`.

PostgreSQL and MongoDB can be staged by client programs installed on the
machine running `resticctl`:

```json
{
  "postgresql_databases": [{
    "name": "accounts",
    "database": "app",
    "host": "db.example.net",
    "port": 5432,
    "username": "backup",
    "globals": true,
    "args": ["--no-owner"]
  }],
  "mongodb_databases": [{
    "name": "events",
    "database": "events",
    "host": "mongo.example.net",
    "config_file": "mongo-backup.yml",
    "args": ["--oplog"]
  }]
}
```

`pg_dump`, `pg_dumpall`, and `mongodump` are resolved on the local `PATH` by
default. Set `executable` or `globals_executable` to an explicit client path.
Hosts may be localhost, remote DNS/IP endpoints, or a supported Unix-socket
path. Client arguments are passed directly without a shell; output, archive,
URI, and password options are reserved so profiles cannot bypass secure
staging or place credentials in process arguments.

Check configuration and database-client availability without connecting to a
database:

```sh
resticctl validate <profile>
```

The same preflight runs before every backup and before installing a backup
schedule. Scheduled jobs still check again when they execute, since their
`PATH` or installed tools may differ later. Explicit absolute client paths are
recommended for scheduled database backups. Forget-only schedules do not
require database clients.

### Profile inheritance

A profile can inherit shared settings from another profile in the same config
directory:

```json
{
  "parent": "shared",
  "repository": "local:/backups/laptop",
  "credentials_file": "laptop.credentials.json",
  "backup_paths": ["~/Documents"],
  "tags": ["laptop"]
}
```

Inheritance may be nested. Scalar fields explicitly present in a child replace
the parent value, including boolean fields set to `false`. Lists such as
`backup_paths`, restic argument lists, tags, and hooks are appended parent-first.
An empty child list therefore adds nothing; it does not clear an inherited
list. SQLite databases are merged by case-insensitive `name`: a child entry
replaces an inherited entry with the same name and new names are appended.
`schedule` and `forget` objects are each inherited or replaced as a whole.

`credentials_file` is never inherited. Every profile used directly must name
its own private credentials file; a profile used only as a parent may omit one.
Parent names use the same portable-name rules as profile names. Missing or
invalid parents and inheritance cycles are rejected, and all validation runs
on the fully merged profile before credentials are loaded or a command runs.

## Credentials

For Backblaze's S3-compatible API, put the application key in the credentials
file:

```json
{
  "environment": {
    "AWS_ACCESS_KEY_ID": "your-key-id",
    "AWS_SECRET_ACCESS_KEY": "your-application-key"
  },
  "password": {
    "command": ["secret-tool", "lookup", "application", "restic", "profile", "<profile>"]
  }
}
```

`password.command` is run directly, without a shell. On macOS, for example:

```json
{
  "password": {
    "command": ["security", "find-generic-password", "-a", "<device>", "-s", "restic-<profile>", "-w"]
  }
}
```

The Keychain entry must exist before running `resticctl init`. Check for it
without printing the password:

```sh
security find-generic-password -a "<device>" -s "restic-<profile>" >/dev/null \
  && echo "Keychain entry found" \
  || echo "Keychain entry not found"
```

Create the entry interactively; macOS prompts for the repository password:

```sh
security add-generic-password -a "<device>" -s "restic-<profile>" -w
```

A password file also works:

```json
{
  "password": {
    "file": "~/.config/resticctl/<profile>.password"
  }
}
```

Set exactly one of `password.command` and `password.file`.

On Unix, profiles, credential files, and password files must be owned by the
current user and inaccessible to group and other users. `resticctl create` uses
mode `0600` for the files it creates.

## Commands

```text
resticctl create <profile>
resticctl list
resticctl init <profile>
resticctl backup <profile> [--dry-run]
resticctl snapshots <profile>
resticctl stats <profile> [--mode <mode>]
resticctl ls <profile> <snapshot> [path...]
resticctl find <profile> <pattern>...
resticctl diff <profile> <snapshot-a> <snapshot-b>
resticctl dump <profile> <snapshot> <path>
resticctl key list <profile>
resticctl key add <profile>
resticctl key remove <profile> <key-id>
resticctl check <profile>
resticctl forget <profile> [--dry-run] [--prune]
resticctl restore <profile> <snapshot> <target> [--dry-run]
resticctl status <profile> [--action backup|forget] [--json]
resticctl schedule install <profile> [backup|forget] [--cron "<expression>"] [--backend auto|cron|launchd] [--catch-up]
resticctl schedule status <profile> [backup|forget] [--json]
resticctl schedule remove <profile> [backup|forget]
resticctl completion <shell>
```

### `create`

Creates a profile JSON file and a matching credentials file. Replace
`<profile>` with a name for the backup.

### `list`

Lists the profiles found in the configuration directory.

### `init`

Initializes the restic repository configured by the profile. Run this once
before the first backup.

### `backup`

Backs up the profile's files and configured SQLite databases. `--dry-run`
passes the option to restic without writing a snapshot.

Backup orchestration is configured with `check_before`, `check_after`,
`prune_before`, and `prune_after`. The order is check-before, prune-before,
backup, check-after, then prune-after; execution stops at the first failure.
Prune options apply `forget_args` to this profile and run Restic `forget
--prune`, so they require non-empty `forget_args`. During `--dry-run`, checks
still run and both backup and retention operations receive `--dry-run`.

SQLite copies exist only for the backup step: resticctl creates consistent
temporary snapshots immediately before Restic backup and removes the staging
directory before any check-after or prune-after operation.

Backup hooks use argument vectors and never invoke a shell implicitly. Each
hook is an object with a non-empty `command` array and an optional Go-style
`timeout` such as `30s` or `5m`; the default timeout is five minutes. Hooks run
in their configured order. `run_before` failures stop the backup,
`run_after` runs only after the full backup workflow succeeds,
`run_after_fail` runs after a before, workflow, or after failure, and
`run_finally` runs on both success and failure. A hook failure is returned to the caller and
stops later hooks in the same phase. Failure and finally hooks are still given
a bounded opportunity to run after cancellation. Temporary SQLite staging and
temporary Restic password files are cleaned before failure/finally processing.

### `snapshots`

`snapshots` lists the backups stored in the repository, including their IDs,
dates, host names, paths, and tags. Use an ID (or `latest`) with `restore` to
retrieve one:

```sh
resticctl snapshots <profile>
resticctl restore <profile> latest <restore-directory>
```

### `check`

Checks the repository for errors. This reads repository data but does not
change retention or remove files.

### Repository inspection

`stats` and `find` restrict snapshot selection to the profile's
`profile:<name>` tag. `ls` and `dump` use that tag when resolving `latest`;
explicit snapshot IDs can refer to any snapshot in the repository. `diff`
compares two explicitly selected snapshots. `dump` writes a selected file to
standard output, or a directory as a tar archive; use `--target` to write it to
a file and `--archive zip` for ZIP output. Run each command with `--help` for
its filtering and formatting flags.

### `key`

`key list` lists the keys that can unlock the repository. `key add` uses
the profile's configured password to unlock the repository, then securely asks
for a new password twice. `key remove` removes the specified key; restic refuses
to remove the key currently being used by the profile. These commands change
repository key metadata, not snapshots or backed-up data.

### `forget`

Applies the profile's retention rules to remove old snapshot references.
`--dry-run` previews the changes. Add `--prune` to remove unreferenced data;
pruning can take a while and requires delete access to the repository.

### `restore`

Restores a snapshot into `<target>`. Use a snapshot ID or `latest` and add
`--dry-run` to preview the restic command.

### `completion`

Generates a shell completion script. Follow the instructions printed by
`resticctl completion --help` to install it for your shell.

## Scheduling and status

Install a daily backup at 02:00:

```sh
resticctl schedule install <profile> --cron "0 2 * * *"
```

The schedule can instead be declared in the profile:

```json
{
  "schedule": {
    "backend": "auto",
    "cron": "0 2 * * *",
    "catch_up": true
  }
}
```

Then install or reconcile the generated scheduler job with:

```sh
resticctl schedule install <profile>
```

Explicit command flags override the corresponding profile values.

Retention can have its own independent schedule:

```json
{
  "forget_args": ["--keep-daily", "7", "--keep-monthly", "12"],
  "forget": {
    "cron": "@daily",
    "backend": "auto",
    "catch_up": true,
    "prune": false
  }
}
```

Install and inspect that job separately:

```sh
resticctl schedule install <profile> forget
resticctl schedule status <profile> forget
```

Set `prune` only when scheduled pruning is intentional; pruning is more
resource-intensive and requires repository delete access.
The older `forget.schedule` field remains accepted as an input alias for
compatibility, but new and generated profiles should use `forget.cron`. Setting
both fields is rejected.

The default `auto` backend uses launchd on macOS and cron on other Unix-like
systems. Windows scheduling is not supported yet. You can select a backend
explicitly with `--backend cron` or `--backend launchd`.

Cron accepts a standard five-field expression. The initial launchd integration
supports a number or `*` in each field; lists, ranges, steps, and named values
are rejected because launchd does not interpret cron syntax directly.
The portable aliases `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`, and
`@annually` are also accepted, as are the same names without `@`. Aliases are
normalized to five-field expressions before installation.

With `catch_up` enabled, resticctl runs at most one overdue backup when its
scheduler starts again. It compares the cron schedule with the last successful
backup; it does not replay every missed occurrence. Cron uses an additional
`@reboot` entry. launchd uses `RunAtLoad` after login and also coalesces calendar
events missed while the laptop was asleep into one event after wake. A
powered-off machine cannot run the job until cron starts during boot or the
launchd agent loads after login.

For an action with no recorded success, catch-up timing starts from the time its
schedule was installed. Installing a launchd job therefore does not immediately
run a new backup or retention operation merely because no prior status exists.

Inspect or remove the schedule with:

```sh
resticctl schedule status <profile>
resticctl schedule remove <profile>
```

Generated jobs contain only the absolute resticctl path, configuration
directory, and profile name. Repository and database credentials are loaded at
runtime and are never written into the scheduler configuration. The current
`PATH` is captured when installing a schedule so the scheduled process can find
restic and configured credential commands; reinstall after changing tool
locations.

On macOS, persistent plist files are installed in
`~/Library/LaunchAgents/io.resticctl.backup.<profile>.plist`. Cron jobs remain in
the current user's crontab. Non-secret installed-schedule metadata remains under
`<config-dir>/schedules/`.

Each non-dry-run backup records its state, start and finish times, duration, and
last successful completion time in a private file under the configuration
directory. Backups for the same profile are locked so overlapping manual and
scheduled runs fail safely. View the latest result with:

```sh
resticctl status <profile>
resticctl status <profile> --action forget
resticctl status <profile> --json
```

Status files intentionally do not store command output or error messages, which
could contain sensitive paths or service details. Dry runs do not replace the
latest real backup status.

## Database backups

Each entry in `sqlite_databases` is copied with SQLite's online backup API and
checked with `PRAGMA integrity_check`. The copies appear in the snapshot as:

```text
databases/<name>.sqlite3
```

The temporary copies are removed when restic finishes or the process receives a
termination signal. A dry run still creates them, but does not keep them.

PostgreSQL custom-format dumps are stored as `databases/<name>.dump`. When
`globals` is enabled, roles and other cluster-wide objects are stored as
`databases/<name>-globals.sql`. After restoring a restic snapshot, restore them
with client tools appropriate to the target server, for example:

```sh
psql --file databases/accounts-globals.sql postgres
pg_restore --dbname app --clean --if-exists databases/accounts.dump
```

MongoDB dumps are stored below `databases/<name>/` and can be restored with:

```sh
mongorestore --config /private/mongo-restore.yml --drop databases/events
```

Database credentials belong in `database_environment` in the private profile
credentials file (for example `{"PGPASSWORD":"..."}` or PostgreSQL service
configuration variables) or, for
MongoDB, in the private YAML file named by `config_file`. They are never added
to generated schedules or client argument values. The MongoDB config file must
be mode 0600 (or otherwise private under the platform checks used for profile
credentials).

`pg_dump` provides a transactionally consistent view of one PostgreSQL
database, but globals are dumped separately and are not atomic with it.
`mongodump` consistency depends on deployment topology: use `--oplog` for a
replica set when a point-in-time dump is required, and consult MongoDB's
requirements and restrictions for sharded clusters. `resticctl` does not
coordinate application writes or transactions across multiple databases.

## Direct restic commands

Use `run` when a Restic command or flag does not have a dedicated resticctl
wrapper. Arguments are passed as an argument vector, preserving their exact
boundaries:

```sh
resticctl run <profile> tag --add old-tag new-tag
resticctl run <profile> unlock --remove-all
```

Supported commands are `backup`, `cache`, `cat`, `check`, `copy`, `diff`,
`dump`, `find`, `forget`, `init`, `key`, `list`, `ls`, `migrate`, `prune`,
`rebuild-index`, `recover`, `repair`, `restore`, `self-update`, `snapshots`,
`stats`, `tag`, and `unlock`. Repository and password-file options are
reserved and cannot be passed through; `resticctl` supplies them securely.
Profile `restic_args` remain global options, while command-specific profile
options and orchestration defaults continue to be applied by their dedicated
commands.

## Support

For restic commands, repositories, and storage backends, see the [restic
documentation](https://restic.readthedocs.io/). For a resticctl bug or feature
request, open an issue in this repository.

## Development

```sh
go test ./...
go vet ./...
```

The regular test suite uses fake runners and temporary local databases. To run
the end-to-end test against an actual temporary restic repository:

```sh
RESTIC_INTEGRATION=1 go test ./internal/app -run TestRealRestic -v
```
