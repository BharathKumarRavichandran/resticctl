package databasebackup

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"

	"resticctl/internal/profile"
)

// Preflight verifies that every database client needed by a profile can be
// resolved in the current process environment. Execution remains authoritative
// because the filesystem or PATH may change after this check.
func Preflight(backupProfile profile.Profile) error {
	return preflight(backupProfile, exec.LookPath)
}

func preflight(backupProfile profile.Profile, lookPath func(string) (string, error)) error {
	requested := make(map[string]string)
	add := func(executable, fallback, purpose string) {
		if executable == "" {
			executable = fallback
		}
		requested[executable] = purpose
	}
	for _, database := range backupProfile.PostgreSQLDatabases {
		add(database.Executable, "pg_dump", "PostgreSQL dumps")
		if database.Globals {
			add(database.GlobalsExecutable, "pg_dumpall", "PostgreSQL globals")
		}
	}
	for _, database := range backupProfile.MongoDBDatabases {
		add(database.Executable, "mongodump", "MongoDB dumps")
	}
	for _, database := range backupProfile.MySQLDatabases {
		add(database.Executable, "mysqldump", "MySQL/MariaDB dumps")
	}
	for _, database := range backupProfile.SQLServerDatabases {
		add(database.Executable, "sqlcmd", "SQL Server dumps")
	}
	var result error
	executables := make([]string, 0, len(requested))
	for executable := range requested {
		executables = append(executables, executable)
	}
	sort.Strings(executables)
	for _, executable := range executables {
		if _, err := lookPath(executable); err != nil {
			result = errors.Join(result, fmt.Errorf("required database client for %s not found: %s", requested[executable], executable))
		}
	}
	return result
}
