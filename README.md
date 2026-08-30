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
  "check_args": [],
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
    "command": ["security", "find-generic-password", "-a", "<DEVICE>", "-s", "restic-<profile>", "-w"]
  }
}
```

The Keychain entry must exist before running `resticctl init`. Check for it
without printing the password:

```sh
security find-generic-password -a "<DEVICE>" -s "restic-<profile>" >/dev/null \
  && echo "Keychain entry found" \
  || echo "Keychain entry not found"
```

Create the entry interactively; macOS prompts for the repository password:

```sh
security add-generic-password -a "<DEVICE>" -s "restic-<profile>" -w
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
resticctl check <profile>
resticctl forget <profile> [--dry-run] [--prune]
resticctl restore <profile> <snapshot> <target> [--dry-run]
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

## SQLite backups

Each entry in `sqlite_databases` is copied with SQLite's online backup API and
checked with `PRAGMA integrity_check`. The copies appear in the snapshot as:

```text
databases/<name>.sqlite3
```

The temporary copies are removed when restic finishes or the process receives a
termination signal. A dry run still creates them, but does not keep them.

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
