package profile

func (configured profileConfig) profile(name string) Profile {
	value := Profile{Name: name, Parent: configured.Parent, BackupPaths: configured.BackupPaths,
		SQLiteDatabases: configured.SQLiteDatabases, ResticArgs: configured.ResticArgs, Commands: configured.Commands,
		PostgreSQLDatabases: configured.PostgreSQLDatabases, MongoDBDatabases: configured.MongoDBDatabases, MySQLDatabases: configured.MySQLDatabases, SQLServerDatabases: configured.SQLServerDatabases,
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
	if configured.PrivateFile != nil {
		value.PrivateFile = *configured.PrivateFile
	}
	if configured.Credentials != nil {
		value.Credentials.Environment = configured.Credentials.Environment
		value.Credentials.Password = configured.Credentials.Password
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
