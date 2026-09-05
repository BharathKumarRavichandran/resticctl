package profile

import (
	"net/url"
	"strings"
)

const (
	redactedValue    = "<redacted>"
	redactedURLValue = "REDACTED"
)

// ResolvedProfile is the effective, secret-minimized profile shown to users.
type ResolvedProfile struct {
	Name        string                 `json:"name"`
	Credentials *RepositoryCredentials `json:"credentials,omitempty"`
	Databases   ResolvedDatabases      `json:"databases"`
	Profile
}

type ResolvedDatabases struct {
	Concurrency int                           `json:"concurrency"`
	SQLite      map[string]SQLiteDatabase     `json:"sqlite,omitempty"`
	PostgreSQL  map[string]PostgreSQLDatabase `json:"postgresql,omitempty"`
	MongoDB     map[string]MongoDBDatabase    `json:"mongodb,omitempty"`
	MySQL       map[string]MySQLDatabase      `json:"mysql,omitempty"`
	SQLServer   map[string]SQLServerDatabase  `json:"sqlserver,omitempty"`
}

// RedactedResolvedProfile returns a copy suitable for display. Public profile
// values remain visible, while fields commonly used to carry secrets do not.
func RedactedResolvedProfile(value Profile) ResolvedProfile {
	credentials := redactedRepositoryCredentials(value.Credentials)
	value.Repository = redactRepository(value.Repository)
	value.CredentialsFile = redactConfigured(value.CredentialsFile)
	value.PrivateFile = redactConfigured(value.PrivateFile)
	value.Monitoring = redactedMonitoring(value.Monitoring)
	value.SQLiteDatabases = append([]SQLiteDatabase(nil), value.SQLiteDatabases...)
	value.PostgreSQLDatabases = append([]PostgreSQLDatabase(nil), value.PostgreSQLDatabases...)
	value.MongoDBDatabases = append([]MongoDBDatabase(nil), value.MongoDBDatabases...)
	value.MySQLDatabases = append([]MySQLDatabase(nil), value.MySQLDatabases...)
	value.SQLServerDatabases = append([]SQLServerDatabase(nil), value.SQLServerDatabases...)
	cloneDatabasePointers(&value)
	// The runtime model intentionally does not retain field-level provenance.
	// When a private overlay is present, redact every field that the overlay is
	// allowed to supply so a public fallback cannot expose its replacement.
	if value.PrivateFile != "" {
		value.Repository = redactedValue
		redactPrivateDatabaseBindings(&value)
	} else {
		removeNormalizedConnectionFields(&value)
	}
	databases := ResolvedDatabases{
		Concurrency: value.DatabaseConcurrency,
		SQLite:      namedDatabaseMap(value.SQLiteDatabases, func(database SQLiteDatabase) string { return database.Name }, func(database *SQLiteDatabase) { database.Name = "" }),
		PostgreSQL:  namedDatabaseMap(value.PostgreSQLDatabases, func(database PostgreSQLDatabase) string { return database.Name }, func(database *PostgreSQLDatabase) { database.Name = "" }),
		MongoDB:     namedDatabaseMap(value.MongoDBDatabases, func(database MongoDBDatabase) string { return database.Name }, func(database *MongoDBDatabase) { database.Name = "" }),
		MySQL:       namedDatabaseMap(value.MySQLDatabases, func(database MySQLDatabase) string { return database.Name }, func(database *MySQLDatabase) { database.Name = "" }),
		SQLServer:   namedDatabaseMap(value.SQLServerDatabases, func(database SQLServerDatabase) string { return database.Name }, func(database *SQLServerDatabase) { database.Name = "" }),
	}
	value.SQLiteDatabases = nil
	value.PostgreSQLDatabases = nil
	value.MongoDBDatabases = nil
	value.MySQLDatabases = nil
	value.SQLServerDatabases = nil
	value.DatabaseConcurrency = 0
	return ResolvedProfile{Name: value.Name, Credentials: credentials, Databases: databases, Profile: value}
}

func namedDatabaseMap[T any](values []T, name func(T) string, clearName func(*T)) map[string]T {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]T, len(values))
	for _, value := range values {
		key := name(value)
		clearName(&value)
		result[key] = value
	}
	return result
}

func cloneDatabasePointers(value *Profile) {
	for i := range value.PostgreSQLDatabases {
		value.PostgreSQLDatabases[i].Connection = cloneConnection(value.PostgreSQLDatabases[i].Connection)
	}
	for i := range value.MongoDBDatabases {
		value.MongoDBDatabases[i].Connection = cloneConnection(value.MongoDBDatabases[i].Connection)
		if value.MongoDBDatabases[i].Options != nil {
			options := *value.MongoDBDatabases[i].Options
			value.MongoDBDatabases[i].Options = &options
		}
	}
	for i := range value.MySQLDatabases {
		value.MySQLDatabases[i].Connection = cloneConnection(value.MySQLDatabases[i].Connection)
	}
	for i := range value.SQLServerDatabases {
		value.SQLServerDatabases[i].Connection = cloneConnection(value.SQLServerDatabases[i].Connection)
	}
}

func cloneConnection(connection *DatabaseConnection) *DatabaseConnection {
	if connection == nil {
		return nil
	}
	result := *connection
	result.Hosts = append([]string(nil), connection.Hosts...)
	if connection.Password != nil {
		password := *connection.Password
		password.Command = append([]string(nil), connection.Password.Command...)
		result.Password = &password
	}
	return &result
}

func removeNormalizedConnectionFields(value *Profile) {
	for i := range value.PostgreSQLDatabases {
		db := &value.PostgreSQLDatabases[i]
		if db.Connection != nil {
			db.Database, db.Host, db.Port, db.Username, db.Hosts = "", "", 0, "", nil
			redactDatabasePassword(value.Credentials, db.Name, db.Connection)
		}
	}
	for i := range value.MongoDBDatabases {
		db := &value.MongoDBDatabases[i]
		if db.Connection != nil {
			db.Database, db.Host, db.Port, db.Username, db.ConfigFile, db.Hosts = "", "", 0, "", "", nil
		}
		if db.Options != nil {
			db.Options.ConfigFile = redactConfigured(db.Options.ConfigFile)
		}
	}
	for i := range value.MySQLDatabases {
		db := &value.MySQLDatabases[i]
		if db.Connection != nil {
			db.Database, db.Host, db.Port, db.Socket, db.Username = "", "", 0, "", ""
			redactDatabasePassword(value.Credentials, db.Name, db.Connection)
		}
	}
	for i := range value.SQLServerDatabases {
		db := &value.SQLServerDatabases[i]
		if db.Connection != nil {
			db.Database, db.Host, db.Port, db.Username = "", "", 0, ""
			redactDatabasePassword(value.Credentials, db.Name, db.Connection)
		}
	}
}

func redactPrivateDatabaseBindings(value *Profile) {
	for i := range value.SQLiteDatabases {
		value.SQLiteDatabases[i].Path = redactedValue
	}
	for i := range value.PostgreSQLDatabases {
		db := &value.PostgreSQLDatabases[i]
		if db.Connection != nil {
			db.Connection = redactedConnection(db.Connection)
			redactDatabasePassword(value.Credentials, db.Name, db.Connection)
			db.Database, db.Host, db.Port, db.Username, db.Hosts = "", "", 0, "", nil
		} else {
			db.Database = redactConfigured(db.Database)
			db.Host = redactConfigured(db.Host)
			db.Username = redactConfigured(db.Username)
			db.Hosts = redactList(db.Hosts)
		}
	}
	for i := range value.MongoDBDatabases {
		db := &value.MongoDBDatabases[i]
		if db.Connection != nil {
			db.Connection = redactedConnection(db.Connection)
			db.Database, db.Host, db.Port, db.Username, db.Hosts = "", "", 0, "", nil
		} else {
			db.Database = redactConfigured(db.Database)
			db.Host = redactConfigured(db.Host)
			db.Username = redactConfigured(db.Username)
			db.Hosts = redactList(db.Hosts)
		}
		if db.Options != nil {
			options := *db.Options
			options.ReplicaSet = redactConfigured(options.ReplicaSet)
			options.ConfigFile = redactConfigured(options.ConfigFile)
			db.Options = &options
			db.ConfigFile = ""
		} else {
			db.ConfigFile = redactConfigured(db.ConfigFile)
		}
	}
	for i := range value.MySQLDatabases {
		db := &value.MySQLDatabases[i]
		if db.Connection != nil {
			db.Connection = redactedConnection(db.Connection)
			redactDatabasePassword(value.Credentials, db.Name, db.Connection)
			db.Database, db.Host, db.Port, db.Socket, db.Username = "", "", 0, "", ""
		} else {
			db.Database = redactConfigured(db.Database)
			db.Host = redactConfigured(db.Host)
			db.Socket = redactConfigured(db.Socket)
			db.Username = redactConfigured(db.Username)
		}
	}
	for i := range value.SQLServerDatabases {
		db := &value.SQLServerDatabases[i]
		if db.Connection != nil {
			db.Connection = redactedConnection(db.Connection)
			redactDatabasePassword(value.Credentials, db.Name, db.Connection)
			db.Database, db.Host, db.Port, db.Username = "", "", 0, ""
		} else {
			db.Database = redactConfigured(db.Database)
			db.Host = redactConfigured(db.Host)
			db.Username = redactConfigured(db.Username)
		}
		db.BackupDirectory = redactConfigured(db.BackupDirectory)
	}
}

func redactedConnection(connection *DatabaseConnection) *DatabaseConnection {
	result := *connection
	result.Database = redactConfigured(result.Database)
	result.Hosts = redactList(result.Hosts)
	result.Socket = redactConfigured(result.Socket)
	result.Username = redactConfigured(result.Username)
	result.Password = nil
	return &result
}

func redactedRepositoryCredentials(credentials Credentials) *RepositoryCredentials {
	if credentials.Environment == nil && !credentials.Password.Configured() {
		return nil
	}
	result := &RepositoryCredentials{Environment: redactedEnvironment(credentials.Environment)}
	if credentials.Password.Configured() {
		result.Password = redactedPasswordSource(credentials.Password)
	}
	return result
}

func redactDatabasePassword(credentials Credentials, name string, connection *DatabaseConnection) {
	credential, ok := credentials.DatabaseCredentialFor(name)
	if ok && credential.Password.Configured() {
		password := redactedPasswordSource(credential.Password)
		connection.Password = &password
	}
}

func redactedPasswordSource(source PasswordSource) PasswordSource {
	result := PasswordSource{}
	if source.Value != "" {
		result.Value = redactedValue
	}
	if source.File != "" {
		result.File = redactedValue
	}
	if source.Command != nil {
		result.Command = []string{redactedValue}
	}
	return result
}

func redactedEnvironment(environment map[string]string) map[string]string {
	if environment == nil {
		return nil
	}
	result := make(map[string]string, len(environment))
	for key := range environment {
		result[key] = redactedValue
	}
	return result
}

func redactConfigured(value string) string {
	if value == "" {
		return ""
	}
	return redactedValue
}

func redactList(values []string) []string {
	if len(values) == 0 {
		return values
	}
	return []string{redactedValue}
}

func redactedMonitoring(value Monitoring) Monitoring {
	if value.Pushgateway != nil {
		gateway := *value.Pushgateway
		gateway.URL = redactEndpoint(gateway.URL)
		gateway.Headers = redactedEnvironment(gateway.Headers)
		value.Pushgateway = &gateway
	}
	value.HTTP = append([]HTTPHook(nil), value.HTTP...)
	for index := range value.HTTP {
		hook := &value.HTTP[index]
		hook.URL = redactEndpoint(hook.URL)
		hook.Headers = redactedEnvironment(hook.Headers)
		if hook.Body != "" {
			hook.Body = redactedValue
		}
		if hook.BodyTemplate != "" {
			hook.BodyTemplate = redactedValue
		}
	}
	return value
}

func redactEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return redactedValue
	}
	if parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" {
		return raw
	}
	return parsed.Scheme + "://" + parsed.Host + "/" + redactedValue
}

func redactRepository(repository string) string {
	separator := strings.Index(repository, "://")
	if separator < 0 {
		return repository
	}
	schemeStart := strings.LastIndex(repository[:separator], ":") + 1
	parsed, err := url.Parse(repository[schemeStart:])
	if err != nil {
		return redactedValue
	}
	redacted := false
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(username, redactedURLValue)
			redacted = true
		}
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = redactedURLValue
		redacted = true
	}
	if parsed.Fragment != "" {
		parsed.Fragment = redactedURLValue
		redacted = true
	}
	if !redacted {
		return repository
	}
	return repository[:schemeStart] + parsed.String()
}
