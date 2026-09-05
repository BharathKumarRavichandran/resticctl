# resticctl

[![CI](https://github.com/BharathKumarRavichandran/resticctl/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/BharathKumarRavichandran/resticctl/actions/workflows/ci.yml)
[![Go 1.25+](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)

A profile-based command-line wrapper around [restic](https://restic.net/) for backups.

A profile keeps the repository, paths, credentials, and retention rules in one place.
resticctl can also stage SQLite, PostgreSQL, MongoDB, MySQL, MariaDB, and SQL Server
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

This creates `<profile>.json` and `<profile>.private.json`. On Linux and macOS they
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
  "private_file": "<profile>.private.json",
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
    "sqlite": {
      "notes": {
        "path": "~/Library/Application Support/notes/data.sqlite3"
      }
    }
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
ultimately decides whether a repeated option is valid. When a child profile
supplies command arguments, its array replaces the inherited command arguments.

`backup_paths` contains ordinary files and directories to include. Database
settings live under `databases`, grouped by provider. For a database-only
profile, set `backup_paths` to `[]`.

Profiles that are committed to source control can keep deployment details in a
private override file:

```json
{
  "repository": "local:development",
  "private_file": "profile.private.json",
  "databases": {
    "postgresql": {
      "accounts": {
        "connection": {
          "database": "development_accounts",
          "hosts": ["localhost:5432"],
          "username": "developer"
        },
        "table_patterns": ["public.customers"]
      }
    }
  }
}
```

The mode-0600 private file supplies or overrides deployment values by logical
database name:

```json
{
  "repository": "s3:https://s3.example.net/private-backups",
  "credentials": {
    "environment": {"AWS_ACCESS_KEY_ID": "...", "AWS_SECRET_ACCESS_KEY": "..."},
    "password": {"command": ["secret-tool", "lookup", "application", "restic"]}
  },
  "databases": {
    "postgresql": {
      "accounts": {
        "connection": {
          "database": "production_accounts",
          "hosts": ["pg1.internal:5432", "pg2.internal:5432"],
          "username": "backup",
          "password": {"file": "/private/postgres-password"}
        }
      }
    }
  }
}
```

The private file follows the same repository, credentials, databases, provider,
and named-entry structure as the public profile. It may contain only deployment
overrides, not backup policy. Provider objects are joined by their backup-name
keys. Private scalar values override public values, while private host lists
replace public host lists. Password sources (`value`,
`file`, or `command`) and the repository `environment` map each replace as one
unit. `private_file` and the legacy
`credentials_file` mode are mutually exclusive. Resolved-profile output keeps
the same shape and renders private values as `"<redacted>"`. The merge uses
strict `encoding/json` decoding and typed merge rules; it does not use a generic
configuration merge library.

PostgreSQL, MongoDB, MySQL, MariaDB, and SQL Server can be staged by client programs
installed on the machine running `resticctl`:

```json
{
  "databases": {
    "concurrency": 2,
    "postgresql": {
      "accounts": {
        "connection": {
          "database": "app",
          "hosts": ["pg1.example.net:5432", "pg2.example.net:5432"],
          "username": "backup"
        },
        "options": {"require_primary": true},
        "globals": true,
        "table_patterns": ["public.customers", "public.orders"],
        "args": ["--no-owner"]
      }
    },
    "mongodb": {
      "events": {
        "connection": {
          "database": "events",
          "hosts": ["mongo1.example.net:27017", "mongo2.example.net:27017"],
          "username": "backup"
        },
        "options": {
          "replica_set": "events-rs",
          "config_file": "mongo-backup.yaml"
        },
        "collection": "activity",
        "exclude_collections": [],
        "args": []
      }
    },
    "mysql": {
      "orders": {
        "connection": {
          "database": "shop",
          "hosts": ["mysql-router.example.net:3306"],
          "username": "backup"
        },
        "routines": true,
        "events": true,
        "triggers": true,
        "tables": []
      }
    },
    "sqlserver": {
      "warehouse": {
        "connection": {
          "database": "reporting",
          "hosts": ["reporting-listener.example.net:1433"],
          "username": "backup"
        },
        "backup_directory": "/var/opt/mssql/backup",
        "compress": true,
        "args": []
      }
    }
  }
}
```

`pg_dump`, `pg_dumpall`, `mongodump`, `mysqldump`, and `sqlcmd` are resolved on the local
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
that many PostgreSQL, MongoDB, MySQL/MariaDB, or SQL Server dumps concurrently; choose a
limit that the database servers and backup host can sustain.

Legacy top-level `database_concurrency`, `sqlite_databases`,
`postgresql_databases`, `mongodb_databases`, `mysql_databases`, and
`sqlserver_databases` fields remain supported for compatibility. Do not mix
them with `databases` in the same profile inheritance chain.

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
the parent value, including boolean fields set to `false`. Every array follows
the same rule: an array supplied by the child replaces the inherited array, an
empty array clears it, and an omitted array preserves it. This applies to paths,
arguments, tags, hooks, host lists, and database selection lists.

Objects merge recursively, including database entries, command sections,
`schedule`, `forget`, and `monitoring`. Entries with keys omitted by the child
remain inherited. Password source objects replace as a unit so sources cannot
be accidentally combined. Set an optional object to `null` to clear it.

`private_file`, `credentials_file`, and inline `credentials` are never inherited.
Every profile used directly must configure one of those credential sources; a
profile used only as a parent may omit them.
Parent names use the same portable-name rules as profile names. Missing or
invalid parents and inheritance cycles are rejected, and all validation runs
on the fully merged profile before credentials are loaded or a command runs.

## Credentials

For Backblaze's S3-compatible API, put the application key in `credentials`
inside the private file. The legacy standalone credentials-file shape is:

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

Set exactly one of `password.value`, `password.file`, or `password.command`.

On Unix, credential files, password files, private override files, and profiles
that contain inline secrets must be owned by the current user and inaccessible
to group and other users. Public profiles without inline secrets may use normal
read permissions. `resticctl create` uses mode `0600` for the files it creates.

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

Creates a profile JSON file and a matching private configuration file. Replace
`<profile>` with a name for the backup.

### `list`

Lists the profiles found in the configuration directory.

### `show`

Prints the fully resolved profile as JSON after inheritance, defaults, path
expansion, and validation. Private values and credential values remain in their
structural locations but are rendered as `"<redacted>"`. Repository URL
passwords, query strings, and fragments and monitoring endpoint paths, query
strings, headers, bodies, and body templates are also redacted. Other public
profile values, including hook and Restic argument vectors, are shown as
configured and must not contain secrets.

### `init`

Initializes the restic repository configured by the profile. Run this once
before the first backup.

### `validate`

Validates the merged profile, credentials, and configured database-client
availability without connecting to a database or repository.

### `backup`

Backs up the profile's files and configured SQLite, PostgreSQL, MongoDB, MySQL,
MariaDB, and SQL Server databases. `--dry-run` passes the option to restic without writing
a snapshot.

Backup orchestration is configured with `check_before`, `check_after`,
`prune_before`, and `prune_after`. The order is check-before, prune-before,
backup, check-after, then prune-after; execution stops at the first failure.
Prune options apply `forget_args` to this profile and run Restic `forget
--prune`, so they require non-empty `forget_args`. During `--dry-run`, checks
still run and both backup and retention operations receive `--dry-run`.

Database staging exists only for the backup step: resticctl creates SQLite
snapshots and PostgreSQL, MongoDB, MySQL/MariaDB, or SQL Server dumps immediately before
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

For a PostgreSQL failover list, `options.require_primary` asks libpq to accept
only a server that supports read-write transactions. This prevents a successful
backup from silently landing on a standby when the listed roles change.

MongoDB dumps are stored below `databases/<name>/` and can be restored with:

```sh
mongorestore --config /private/mongo-restore.yaml --drop databases/events
```

MySQL and MariaDB dumps are stored as `databases/<name>.sql`. Restore with the
matching client and a private option file:

```sh
mysql --defaults-extra-file=/private/mysql-restore.cnf shop < databases/orders.sql
# MariaDB: mariadb --defaults-extra-file=/private/mysql-restore.cnf shop < databases/orders.sql
```

SQL Server native backups are stored as `databases/<name>.bak`. Restore them
with SQL Server tooling after copying the file to a path visible to the target
server.

In legacy `credentials_file` mode, database credentials are scoped by configured
database backup name under `databases`:

```json
{
  "databases": {
    "accounts": {"password": {"value": "..."}},
    "orders": {"password": {"file": "/private/mysql-password"}},
    "warehouse": {"password": {"command": ["secret-tool", "lookup", "database", "warehouse"]}}
  }
}
```

Each entry is available only to the database backup with the same `name`.
resticctl delivers `password` using the provider's secure mechanism rather than
requiring users to know client-specific environment variables. The optional
`environment` map is an advanced escape hatch. The deprecated
`database_environment` and `database_environments` fields remain readable for
compatibility, but cannot be mixed with `databases`. MongoDB passwords live in
the private YAML file named by `options.config_file`; its credential entry may
contain only additional environment values. Credentials are never added to
generated schedules or client argument values. The MongoDB config file must be
mode 0600 (or otherwise private under the platform checks used for profile
credentials).

For MySQL/MariaDB, resticctl writes the password and configured
`username` to a temporary mode-0600 client option file, passes that file as the
client's first option, blanks inherited `MYSQL_PWD`, and removes the file as
soon as the dump exits. Use `socket` for a Unix socket, or `host` and optional
`port` for TCP; `host` and `socket` are mutually exclusive. PostgreSQL
`table_patterns` use `pg_dump` pattern semantics. MySQL/MariaDB `tables` contain
literal table names. MongoDB `collection` limits an entry to one named
collection, while `exclude_collections` dumps the database in one invocation
but omits the listed collections. These fields are mutually exclusive. Empty
or omitted selection fields dump the whole configured database. Routines,
events, and triggers are excluded from MySQL/MariaDB dumps unless their
corresponding booleans are enabled.

SQL Server backups use `sqlcmd` to run `BACKUP DATABASE` with `COPY_ONLY` and
`CHECKSUM`; `compress` adds `COMPRESSION`. The configured database name is
quoted as an identifier, and `SQLSERVER_PASSWORD` is exposed to the client only
as `SQLCMDPASSWORD`. Because SQL Server writes native backups on the server,
`backup_directory` must be an existing directory visible at the same absolute
path to both SQL Server and resticctl. The provider copies the completed backup
into private Restic staging and removes the shared temporary file. On Unix the
directory must not be accessible by users outside its owner and group; on
Windows it must have an explicit, inheritance-protected ACL. A typical
ACL must not grant access to Everyone, Authenticated Users, or the built-in
Users group. A typical remote SQL Server without a shared filesystem is not supported. The login needs
permission to back up the database, and the SQL Server service needs write
access to the configured directory.
For an Availability Group, configure its listener as the single host rather
than listing individual nodes. SQLCMD variants do not implement multi-subnet
behavior consistently, so resticctl does not currently expose that option.

`pg_dump` provides a transactionally consistent view of one PostgreSQL
database, but globals are dumped separately and are not atomic with it.
Selected PostgreSQL table patterns might not include dependent objects needed
for an independent restore. MongoDB `collection` and `exclude_collections`
cannot be combined with `--oplog` or other selection arguments. Configure
separately named MongoDB entries for separate included collections so each dump
has an explicit artifact and consistency boundary. Partial dumps include a
sibling `databases/<name>.selection.json` manifest when selection is configured
through these first-class fields. The manifest records the provider, database,
and configured selection.

`mongodump` consistency depends on deployment topology: use `--oplog` for a
replica set when a point-in-time dump is required, and consult MongoDB's
requirements and restrictions for sharded clusters. `resticctl` does not
coordinate application writes or transactions across multiple databases.
MySQL/MariaDB dumps always use `--single-transaction`, which gives a consistent
snapshot for transactional tables such as InnoDB without blocking writers.
Non-transactional tables such as MyISAM are not made transactionally consistent;
quiesce writes or arrange the required server-side locks before running the
backup. SQL Server produces a full native database backup, not a table-level
export. A dump is not atomic with separately configured databases.

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
