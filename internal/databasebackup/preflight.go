package databasebackup

import (
	"errors"
	"fmt"
	"os/exec"

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
	for _, database := range backupProfile.PostgreSQLDatabases {
		executable := database.Executable
		if executable == "" {
			executable = "pg_dump"
		}
		requested[executable] = "PostgreSQL dumps"
		if database.Globals {
			globalsExecutable := database.GlobalsExecutable
			if globalsExecutable == "" {
				globalsExecutable = "pg_dumpall"
			}
			requested[globalsExecutable] = "PostgreSQL globals"
		}
	}
	for _, database := range backupProfile.MongoDBDatabases {
		executable := database.Executable
		if executable == "" {
			executable = "mongodump"
		}
		requested[executable] = "MongoDB dumps"
	}
	for _, database := range backupProfile.MySQLDatabases {
		executable := database.Executable
		if executable == "" {
			executable = "mysqldump"
		}
		requested[executable] = "MySQL/MariaDB dumps"
	}
	var result error
	for executable, purpose := range requested {
		if _, err := lookPath(executable); err != nil {
			result = errors.Join(result, fmt.Errorf("required database client for %s not found: %s", purpose, executable))
		}
	}
	return result
}
