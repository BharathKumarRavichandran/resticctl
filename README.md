# resticctl

[![CI](https://github.com/BharathKumarRavichandran/resticctl/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/BharathKumarRavichandran/resticctl/actions/workflows/ci.yml)
[![Go 1.25+](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)

A profile-based command-line wrapper around [restic](https://restic.net/) for backups.

A profile keeps the repository, paths, credentials, and retention rules in one place.
resticctl can also stage SQLite, PostgreSQL, MongoDB, MySQL, and MariaDB
databases, adding their snapshots or dumps to the same Restic snapshot as the
other files.

Restic handles the backup, encryption, restore, and repository maintenance.

## Requirements

- restic
- Go 1.25 or newer to build from source

Build the binary from the repository root:

```sh
go build -o resticctl ./cmd/resticctl
```

On Windows, use `go build -o resticctl.exe ./cmd/resticctl`.

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
  "commands": {
    "backup": {"args": ["--exclude-caches", "--skip-if-unchanged"]},
    "mount": {"args": ["--allow-other"]},
    "rewrite": {"args": ["--dry-run"]}
  },
  "backup_paths": ["~/Documents", "~/Pictures"],
  "backup_args": [],
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
  "databases": {
    "sqlite": [{
      "name": "notes",
      "path": "~/Library/Application Support/notes/data.sqlite3"
    }]
  }
}
```

`restic_args` are Restic global flags and are placed before every supported
Restic subcommand. `commands` contains persistent, command-specific raw argument
vectors. The legacy `backup_args`, `check_args`, and `forget_args` fields remain
supported for compatibility.

Arguments are assembled in increasing precedence: resticctl orchestration
defaults, legacy command arguments, the matching `commands` section, then CLI
arguments. When an option is repeated, the CLI value is therefore last; Restic
ultimately decides whether a repeated option is valid. Parent profile command
arguments are appended before child arguments. Use `"commands"` in
`replace_inherited` to replace all inherited command sections.

`backup_paths` contains ordinary files and directories to include. Database
settings live under `databases`, grouped by provider. For a database-only
profile, set `backup_paths` to `[]`.

PostgreSQL, MongoDB, MySQL, and MariaDB can be staged by client programs
installed on the machine running `resticctl`:

```json
{
  "databases": {
    "concurrency": 2,
    "postgresql": [{
      "name": "accounts",
      "database": "app",
      "host": "db.example.net",
      "port": 5432,
      "username": "backup",
      "globals": true,
      "table_patterns": ["public.customers", "public.orders"],
      "args": ["--no-owner"]
    }],
    "mongodb": [{
      "name": "events",
      "database": "events",
      "host": "mongo.example.net",
      "config_file": "mongo-backup.yml",
      "collection": "activity",
      "args": []
    }],
    "mysql": [{
      "name": "orders",
      "database": "shop",
      "host": "mysql.example.net",
      "port": 3306,
      "username": "backup",
      "routines": true,
      "events": true,
      "triggers": true,
      "tables": []
    }]
  }
}
```

`pg_dump`, `pg_dumpall`, `mongodump`, and `mysqldump` are resolved on the local
`PATH` by default. Set `executable` or `globals_executable` to an explicit
client path.
Set a MySQL entry's `executable` to `mariadb-dump` when appropriate.
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
require database clients. External database dumps run sequentially by default.
Set `databases.concurrency` to a positive value greater than one to run at most
that many PostgreSQL, MongoDB, or MySQL/MariaDB dumps concurrently; choose a
limit that the database servers and backup host can sustain.

Legacy top-level `database_concurrency`, `sqlite_databases`,
`postgresql_databases`, `mongodb_databases`, and `mysql_databases` fields remain
supported for compatibility. Do not mix them with `databases` in one profile.

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
list. Entries in each of the SQLite, PostgreSQL, MongoDB, and MySQL database
lists are merged by case-insensitive `name`: a child entry replaces an inherited entry
with the same name and new names are appended.
`schedule` and `forget` objects are each inherited or replaced as a whole.

To replace or clear inherited collections explicitly, list their JSON field
names in `replace_inherited`. The child value then replaces the parent instead
of being appended; an empty child list clears it. Listing `schedule` or `forget`
clears an inherited schedule when the child omits that object. For example:

```json
{
  "parent": "shared",
  "credentials_file": "laptop.credentials.json",
  "replace_inherited": ["backup_paths", "tags", "run_before", "schedule"],
  "backup_paths": ["~/Documents"],
  "tags": [],
  "run_before": []
}
```

For nested database lists, use `databases.sqlite`, `databases.postgresql`,
`databases.mongodb`, or `databases.mysql` in `replace_inherited`. Supported
replacement fields are the backup paths, all four database lists,
Restic argument lists, tags, hook lists, and the `schedule` and `forget`
objects. Unknown or duplicate names are rejected.

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
resticctl show <profile>
resticctl init <profile>
resticctl validate <profile>
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
resticctl status <profile> [--action backup|check|forget|prune|copy] [--history N] [--json]
resticctl schedule install <profile> [backup|check|forget|prune|copy] [--calendar "<expression>" ...] [--backend auto|cron|launchd|systemd|windows] [--catch-up] [--dry-run]
resticctl schedule reconcile <profile> [--dry-run]
resticctl schedule reconcile --all [--dry-run]
resticctl schedule list [profile] [--json]
resticctl schedule status <profile> [backup|check|forget|prune|copy] [--json]
resticctl schedule remove <profile> [backup|check|forget|prune|copy] [--dry-run]
resticctl run <profile> <restic-command> [args...]
resticctl completion <shell>
```

### `create`

Creates a profile JSON file and a matching credentials file. Replace
`<profile>` with a name for the backup.

### `list`

Lists the profiles found in the configuration directory.

### `show`

Prints the fully resolved profile as JSON after inheritance, defaults, path
expansion, and validation. Loaded credentials are omitted. Repository URL
passwords, query strings, and fragments and monitoring endpoint paths, query
strings, headers, bodies, and body templates are redacted. Other public profile
values, including hook and Restic argument vectors, are shown as configured and
must not contain secrets.

### `init`

Initializes the restic repository configured by the profile. Run this once
before the first backup.

### `validate`

Validates the merged profile, credentials, and configured database-client
availability without connecting to a database or repository.

### `backup`

Backs up the profile's files and configured SQLite, PostgreSQL, MongoDB, MySQL,
and MariaDB databases. `--dry-run` passes the option to restic without writing
a snapshot.

Backup orchestration is configured with `check_before`, `check_after`,
`prune_before`, and `prune_after`. The order is check-before, prune-before,
backup, check-after, then prune-after; execution stops at the first failure.
Prune options apply `forget_args` to this profile and run Restic `forget
--prune`, so they require non-empty `forget_args`. During `--dry-run`, checks
still run and both backup and retention operations receive `--dry-run`.

Database staging exists only for the backup step: resticctl creates SQLite
snapshots and PostgreSQL, MongoDB, or MySQL/MariaDB dumps immediately before
Restic backup, then removes the staging directory before any check-after or
prune-after operation.

Backup hooks use argument vectors and never invoke a shell implicitly. Each
hook is an object with a non-empty `command` array and an optional Go-style
`timeout` such as `30s` or `5m`; the default timeout is five minutes. Hooks run
in their configured order. Runtime source and database-client checks happen
after `run_before`, allowing it to mount or create a configured source.
`run_before` failures stop the backup, `run_after` runs only after the full
backup workflow succeeds, `run_after_fail` runs when a before hook, runtime
validation, workflow, or after hook fails, and `run_finally` runs on both
success and failure. A hook failure is returned to the caller and stops later
hooks in the same phase. Failure and finally hooks are still given a bounded
opportunity to run after cancellation. Temporary database staging and Restic
password files are cleaned before failure/finally processing.

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

Then install the generated scheduler job with:

```sh
resticctl schedule install <profile>
```

Reconcile every schedule declared by one profile, or by every discovered
profile, with:

```sh
resticctl schedule reconcile <profile>
resticctl schedule reconcile --all
```

Reconciliation installs or updates the declared backup and forget schedules.
It also removes a previously installed backup or forget schedule when the
corresponding profile object has been removed. Use `--dry-run` to render jobs
and report removals without changing scheduler or state files. Scheduler policy
set during installation, such as permission, logging, locking, and activation,
is preserved when the profile schedule is reconciled.

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

The default `auto` backend uses launchd on macOS, Windows Task Scheduler on
Windows, and cron on other Unix-like systems. Native systemd timers are
available explicitly with `--backend systemd`. Repeat `--calendar` to attach
multiple calendars to the same action; `--cron` remains a compatible alias for
one expression. `--dry-run` renders the definition without writing scheduler
or state files.

The portable calendar syntax is a standard five-field cron expression in local
time: minute, hour, day of month, month, and day of week. Cron preserves the
expression. systemd translates all five fields to `OnCalendar`; numeric cron
weekday values are retained, so use numeric values for portability. launchd
supports only a number or `*` in each field. Windows currently supports daily
or hourly expressions (day, month, and weekday must be `*`). Lists, ranges,
steps, and names are therefore not portable and are rejected by backends that
cannot represent them.
The portable aliases `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`, and
`@annually` are also accepted, as are the same names without `@`. Aliases are
normalized to five-field expressions before installation.

With `catch_up` enabled, resticctl runs at most one overdue backup or retention
action when its scheduler starts again. It compares the cron schedule with the
last successful run of that action; it does not replay every missed occurrence.
Cron uses an additional `@reboot` entry. launchd uses `RunAtLoad` after login
and also coalesces calendar events missed while the laptop was asleep into one
event after wake. A powered-off machine cannot run the job until cron starts
during boot or the launchd agent loads after login.

For an action with no recorded success, catch-up timing starts from the time its
schedule was installed. Installing a launchd job therefore does not immediately
run a new backup or retention operation merely because no prior status exists.

Inspect or remove the schedule with:

```sh
resticctl schedule list [profile] [--json]
resticctl schedule status <profile>
resticctl schedule remove <profile>
resticctl schedule uninstall <profile>
```

`schedule status` verifies that the recorded cron entry or loaded launchd job
still exists and reports drift instead of trusting its state file alone. Run
`schedule install` again to reconcile a missing or edited job.
`schedule list` verifies every recorded job and reports each as `ok` or
`drift`. `uninstall` is an alias for `remove`, and removal supports `--dry-run`.

Generated jobs contain only the absolute resticctl path, configuration
directory, profile name, and scheduled action. Repository and database
credentials are loaded at runtime and are never written into the scheduler
configuration. The current `PATH` is captured when installing a schedule so
the scheduled process can find restic and configured credential commands;
reinstall after changing tool locations.

On macOS, persistent plist files are installed as
`~/Library/LaunchAgents/io.resticctl.<action>.<profile>.plist`. Cron jobs remain
in the current user's crontab. `--crontab-file` targets an explicit file; with
`--permission system`, each entry includes the required `--user` column.
systemd supports user and system units, while Task Scheduler supports
logged-on-user and system principals. `--no-start` and `--no-enable` control
activation where the backend separates installation from activation.

`--priority background`, `--log`, `--lock-mode wait --lock-wait <duration>`,
`--require-network`, and `--require-ac-power` configure execution policy.
Availability conditions are emitted only using facilities offered by the
selected backend; cron cannot enforce network or power conditions itself.
Non-secret installed-schedule metadata remains
under `<config-dir>/schedules/`.

Each non-dry-run backup, check, forget, prune, or copy action records its
command, state, start and finish times, duration, exit code, error category,
Restic warning state, and last successful completion time in a private file
under the configuration directory. A bounded per-command history is retained;
operations for the same profile are locked so overlapping runs fail safely.
View the latest result or history with:

```sh
resticctl status <profile>
resticctl status <profile> --action forget
resticctl status <profile> --action backup --history 10
resticctl status <profile> --json
```

Status files intentionally do not store command output, error messages,
repository locations, source paths, credential values, sensitive HTTP headers,
or temporary credential-file names. Dry runs do not replace status.

### Monitoring, notifications, and logs

The optional `monitoring` object controls external status export. All delivery
failures are non-fatal: they never change or hide the result of the Restic
action. For example:

```json
{
  "monitoring": {
    "history_limit": 100,
    "warning_policy": "warning",
    "backup_statistics": true,
    "status_file": "/var/lib/node-exporter/resticctl.json",
    "prometheus_textfile": "/var/lib/node-exporter/resticctl.prom",
    "pushgateway": {
      "url": "https://push.example.invalid",
      "job": "resticctl",
      "labels": {"site": "primary"},
      "timeout": "10s",
      "headers": {"Authorization": "Bearer <token>"},
      "ca_file": "monitoring-ca.pem"
    },
    "http": [{
      "name": "operations",
      "url": "https://monitor.example.invalid/restic",
      "actions": ["backup", "check", "forget", "prune", "copy"],
      "phases": ["send-before", "send-after", "send-after-fail", "send-finally", "warning"],
      "method": "POST",
      "headers": {"Authorization": "Bearer <token>"},
      "body_template": "{\"profile\":{{printf \"%q\" .Status.Profile}},\"state\":{{printf \"%q\" .Status.State}}}",
      "timeout": "10s",
      "ca_file": "monitoring-ca.pem"
    }],
    "logs": [
      {"type": "console"},
      {"type": "file", "path": "resticctl-events.jsonl"},
      {"type": "local-syslog"},
      {"type": "remote-syslog", "network": "udp", "address": "logs.example.invalid:514"}
    ]
  }
}
```

Relative output and CA paths resolve beside the profile. Output files and log
files are private. HTTP targets support `GET`, `POST`, `PUT`, and `PATCH`;
omitting `phases` selects `send-finally`, and omitting `actions` selects every
recorded action. `body` sends literal content, while `body_template` uses Go's
data-only text templating against `.Phase` and `.Status`. It cannot execute
commands or access the loaded profile and credentials. Custom CA files must be
regular files and are bounded before loading.

Restic exit code 3 is governed by `warning_policy`: `failure` returns a failed
action, `warning` returns success with status state `warning`, and `success`
returns success with state `succeeded`. All three retain
`restic_warning: true`; HTTP targets with phase `warning` act as non-fatal
warning handlers. When `backup_statistics` is enabled, resticctl requests
Restic JSON output and stores only aggregate counters and byte totals, never
snapshot paths or IDs. JSON status, Prometheus textfiles, and Pushgateway
metrics contain the same redacted status model.

## Database backups

Each entry in `databases.sqlite` is copied with SQLite's online backup API and
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

MySQL and MariaDB dumps are stored as `databases/<name>.sql`. Restore with the
matching client and a private option file:

```sh
mysql --defaults-extra-file=/private/mysql-restore.cnf shop < databases/orders.sql
# MariaDB: mariadb --defaults-extra-file=/private/mysql-restore.cnf shop < databases/orders.sql
```

Database credentials should be scoped by configured database name in
`database_environments` in the private credentials file:

```json
{
  "database_environments": {
    "accounts": {"PGPASSWORD": "..."},
    "events": {"MONGO_TOKEN": "..."},
    "orders": {"MYSQL_PASSWORD": "..."}
  }
}
```

Each named environment is made available only to that database provider. The older
`database_environment` field remains available for values intentionally shared
by every database client; named values override shared values. MongoDB secrets
may instead live in the private YAML file named by `config_file`. Credentials
are never added to generated schedules or client argument values. The MongoDB
config file must be mode 0600 (or otherwise private under the platform checks
used for profile credentials).

For MySQL/MariaDB, resticctl writes `MYSQL_PASSWORD` and the configured
`username` to a temporary mode-0600 client option file, passes that file as the
client's first option, blanks inherited `MYSQL_PWD`, and removes the file as
soon as the dump exits. Use `socket` for a Unix socket, or `host` and optional
`port` for TCP; `host` and `socket` are mutually exclusive. PostgreSQL
`table_patterns` use `pg_dump` pattern semantics. MySQL/MariaDB `tables` contain
literal table names. MongoDB `collection` limits an entry to one named
collection. Empty or omitted selection fields dump the whole configured
database. Routines, events, and triggers are excluded from MySQL/MariaDB dumps
unless their corresponding booleans are enabled.

`pg_dump` provides a transactionally consistent view of one PostgreSQL
database, but globals are dumped separately and are not atomic with it.
Selected PostgreSQL table patterns might not include dependent objects needed
for an independent restore. A selected MongoDB collection cannot be combined
with `--oplog` or other selection arguments. Configure separately named MongoDB
entries for separate collections so each dump has an explicit artifact and
consistency boundary. Partial dumps include a sibling
`databases/<name>.selection.json` manifest recording their provider, database,
and configured selection.
`mongodump` consistency depends on deployment topology: use `--oplog` for a
replica set when a point-in-time dump is required, and consult MongoDB's
requirements and restrictions for sharded clusters. `resticctl` does not
coordinate application writes or transactions across multiple databases.
MySQL/MariaDB dumps always use `--single-transaction`, which gives a consistent
snapshot for transactional tables such as InnoDB without blocking writers.
Non-transactional tables such as MyISAM are not made transactionally consistent;
quiesce writes or arrange the required server-side locks before running the
backup. A dump is not atomic with separately configured databases.

## Direct restic commands

Use `run` when a Restic command or flag does not have a dedicated resticctl
wrapper. Arguments are passed as an argument vector, preserving their exact
boundaries:

```sh
resticctl run <profile> tag --add old-tag new-tag
resticctl run <profile> unlock --remove-all
resticctl run <profile> mount /mnt/backup
resticctl run <profile> rewrite --dry-run
```

Supported commands are `backup`, `cache`, `cat`, `check`, `copy`, `diff`,
`dump`, `features`, `find`, `forget`, `generate`, `init`, `key`, `list`, `ls`,
`migrate`, `mount`, `options`, `prune`, `rebuild-index`, `recover`, `repair`,
`restore`, `rewrite`, `self-update`, `snapshots`, `stats`, `tag`, `unlock`, and
`version`. Persistent sections may also target `key add`, `key list`, `key
passwd`, `key remove`, `repair index`, `repair packs`, and `repair snapshots`.
The explicit catalog prevents misspelled commands from being silently passed
through; supporting a new Restic command requires adding it to the catalog.
New flags need no resticctl change and can be placed in a command section or on
the `run` command line.

Repository and password-source options, including attached short forms such as
`-r/path` and `-p/path`, are reserved and cannot be passed through; resticctl
supplies them securely. Arguments after `--` are treated as positional values.
To see version-matched help, run `resticctl run <profile> <restic-command>
--help`. Because `run` stops interpreting flags at the pass-through boundary,
put global resticctl flags before `run`, for example `resticctl --config-dir
/etc/resticctl run home mount --help`.

## Support

For restic commands, repositories, and storage backends, see the [restic
documentation](https://restic.readthedocs.io/). For a resticctl bug or feature
request, open an issue in this repository.

## Development

Use Go 1.25 or newer. Run the checks from the repository root:

```sh
go test ./...
go vet ./...
```

The regular test suite uses fake runners and temporary local databases. To run
the end-to-end test against an actual temporary restic repository:

```sh
RESTIC_INTEGRATION=1 go test ./internal/app -run TestRealRestic -v
```

Build a development binary with:

```sh
go build -o resticctl ./cmd/resticctl
```

Development builds report the version as `dev`. Release builds can inject a
version at link time:

```sh
go build -ldflags "-X main.version=v0.1.0" -o resticctl ./cmd/resticctl
```

When installed from a tagged Go module version, `resticctl` also reads the
version from Go build metadata.
