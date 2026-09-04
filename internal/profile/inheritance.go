package profile

import (
	"fmt"
	"slices"
	"strings"
)

func merge(parent, child profileConfig) profileConfig {
	result := parent
	result.Parent = child.Parent
	result.ReplaceInherited = nil
	if child.Repository != nil {
		result.Repository = child.Repository
	}
	// Credentials deliberately belong to the requested profile and are never inherited.
	result.CredentialsFile = child.CredentialsFile
	result.BackupPaths = mergeList(parent.BackupPaths, child.BackupPaths, child.replaces("backup_paths"))
	result.SQLiteDatabases = mergeNamed(parent.SQLiteDatabases, child.SQLiteDatabases, func(v SQLiteDatabase) string { return v.Name }, child.replaces("sqlite_databases"))
	result.PostgreSQLDatabases = mergeNamed(parent.PostgreSQLDatabases, child.PostgreSQLDatabases, func(v PostgreSQLDatabase) string { return v.Name }, child.replaces("postgresql_databases"))
	result.MongoDBDatabases = mergeNamed(parent.MongoDBDatabases, child.MongoDBDatabases, func(v MongoDBDatabase) string { return v.Name }, child.replaces("mongodb_databases"))
	result.MySQLDatabases = mergeNamed(parent.MySQLDatabases, child.MySQLDatabases, func(v MySQLDatabase) string { return v.Name }, child.replaces("mysql_databases"))
	result.ResticArgs = mergeList(parent.ResticArgs, child.ResticArgs, child.replaces("restic_args"))
	result.Commands = mergeCommands(parent.Commands, child.Commands, child.replaces("commands"))
	result.BackupArgs = mergeList(parent.BackupArgs, child.BackupArgs, child.replaces("backup_args"))
	result.Tags = mergeList(parent.Tags, child.Tags, child.replaces("tags"))
	result.ForgetArgs = mergeList(parent.ForgetArgs, child.ForgetArgs, child.replaces("forget_args"))
	result.CheckArgs = mergeList(parent.CheckArgs, child.CheckArgs, child.replaces("check_args"))
	if child.CheckBefore != nil {
		result.CheckBefore = child.CheckBefore
	}
	if child.CheckAfter != nil {
		result.CheckAfter = child.CheckAfter
	}
	if child.PruneBefore != nil {
		result.PruneBefore = child.PruneBefore
	}
	if child.PruneAfter != nil {
		result.PruneAfter = child.PruneAfter
	}
	if child.DatabaseConcurrency != nil {
		result.DatabaseConcurrency = child.DatabaseConcurrency
	}
	result.RunBefore = mergeList(parent.RunBefore, child.RunBefore, child.replaces("run_before"))
	result.RunAfter = mergeList(parent.RunAfter, child.RunAfter, child.replaces("run_after"))
	result.RunAfterFail = mergeList(parent.RunAfterFail, child.RunAfterFail, child.replaces("run_after_fail"))
	result.RunFinally = mergeList(parent.RunFinally, child.RunFinally, child.replaces("run_finally"))
	if child.Schedule != nil || child.replaces("schedule") {
		result.Schedule = child.Schedule
	}
	if child.Forget != nil || child.replaces("forget") {
		result.Forget = child.Forget
	}
	if child.Monitoring != nil || child.replaces("monitoring") {
		result.Monitoring = child.Monitoring
	}
	return result
}

func mergeList[T any](parent, child []T, replace bool) []T {
	if replace {
		return append([]T(nil), child...)
	}
	return append(append([]T(nil), parent...), child...)
}

func mergeNamed[T any](parent, child []T, name func(T) string, replace bool) []T {
	if replace {
		return append([]T(nil), child...)
	}
	result := append([]T(nil), parent...)
	positions := make(map[string]int, len(result))
	for i, item := range result {
		positions[strings.ToLower(name(item))] = i
	}
	for _, item := range child {
		key := strings.ToLower(name(item))
		if i, ok := positions[key]; ok {
			result[i] = item
		} else {
			positions[key] = len(result)
			result = append(result, item)
		}
	}
	return result
}

func mergeCommands(parent, child map[string]ResticCommand, replace bool) map[string]ResticCommand {
	result := make(map[string]ResticCommand, len(parent)+len(child))
	if !replace {
		for name, command := range parent {
			result[name] = ResticCommand{Args: append([]string(nil), command.Args...)}
		}
	}
	for name, command := range child {
		if inherited, ok := result[name]; ok {
			command.Args = mergeList(inherited.Args, command.Args, false)
		} else {
			command.Args = append([]string(nil), command.Args...)
		}
		result[name] = command
	}
	return result
}

func (configured profileConfig) replaces(field string) bool {
	return slices.Contains(configured.ReplaceInherited, field)
}

func validateReplaceInherited(fields []string) error {
	allowed := map[string]struct{}{
		"backup_paths": {}, "sqlite_databases": {}, "postgresql_databases": {}, "mongodb_databases": {}, "mysql_databases": {},
		"restic_args": {}, "commands": {}, "backup_args": {}, "tags": {}, "forget_args": {}, "check_args": {},
		"run_before": {}, "run_after": {}, "run_after_fail": {}, "run_finally": {},
		"schedule": {}, "forget": {}, "monitoring": {},
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("replace_inherited contains unsupported field %q", field)
		}
		if _, ok := seen[field]; ok {
			return fmt.Errorf("replace_inherited contains duplicate field %q", field)
		}
		seen[field] = struct{}{}
	}
	return nil
}

func (configured profileConfig) profile(name string) Profile {
	value := Profile{Name: name, Parent: configured.Parent, BackupPaths: configured.BackupPaths,
		SQLiteDatabases: configured.SQLiteDatabases, ResticArgs: configured.ResticArgs, Commands: configured.Commands,
		PostgreSQLDatabases: configured.PostgreSQLDatabases, MongoDBDatabases: configured.MongoDBDatabases, MySQLDatabases: configured.MySQLDatabases,
		BackupArgs: configured.BackupArgs, Tags: configured.Tags, ForgetArgs: configured.ForgetArgs,
		CheckArgs: configured.CheckArgs, RunBefore: configured.RunBefore, RunAfter: configured.RunAfter,
		RunAfterFail: configured.RunAfterFail, RunFinally: configured.RunFinally,
		Schedule: configured.Schedule, Forget: configured.Forget}
	if configured.Monitoring != nil {
		value.Monitoring = *configured.Monitoring
	}
	if configured.Repository != nil {
		value.Repository = *configured.Repository
	}
	if configured.CredentialsFile != nil {
		value.CredentialsFile = *configured.CredentialsFile
	}
	value.DatabaseConcurrency = 1
	if configured.DatabaseConcurrency != nil {
		value.DatabaseConcurrency = *configured.DatabaseConcurrency
	}
	if configured.CheckBefore != nil {
		value.CheckBefore = *configured.CheckBefore
	}
	if configured.CheckAfter != nil {
		value.CheckAfter = *configured.CheckAfter
	}
	if configured.PruneBefore != nil {
		value.PruneBefore = *configured.PruneBefore
	}
	if configured.PruneAfter != nil {
		value.PruneAfter = *configured.PruneAfter
	}
	return value
}
