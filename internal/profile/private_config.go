package profile

import (
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"resticctl/internal/secretvalue"
)

type privateConfig struct {
	Repository  *string                       `json:"repository,omitempty"`
	Credentials *privateRepositoryCredentials `json:"credentials,omitempty"`
	Databases   *privateDatabases             `json:"databases,omitempty"`
}

type privateRepositoryCredentials struct {
	Environment *map[string]string `json:"environment,omitempty"`
	Password    *PasswordSource    `json:"password,omitempty"`
}

type privateDatabases struct {
	SQLite     map[string]privateSQLiteDatabase     `json:"sqlite,omitempty"`
	PostgreSQL map[string]privatePostgreSQLDatabase `json:"postgresql,omitempty"`
	MongoDB    map[string]privateMongoDBDatabase    `json:"mongodb,omitempty"`
	MySQL      map[string]privateMySQLDatabase      `json:"mysql,omitempty"`
	SQLServer  map[string]privateSQLServerDatabase  `json:"sqlserver,omitempty"`
}

type privateSQLiteDatabase struct {
	Path *string `json:"path,omitempty"`
}

type privatePostgreSQLDatabase struct {
	Connection *privateConnection        `json:"connection,omitempty"`
	Options    *privatePostgreSQLOptions `json:"options,omitempty"`
}

type privateMongoDBDatabase struct {
	Connection *privateConnection     `json:"connection,omitempty"`
	Options    *privateMongoDBOptions `json:"options,omitempty"`
}

type privateMySQLDatabase struct {
	Connection *privateConnection `json:"connection,omitempty"`
}

type privateSQLServerDatabase struct {
	Connection      *privateConnection `json:"connection,omitempty"`
	BackupDirectory *string            `json:"backup_directory,omitempty"`
}

type privateConnection struct {
	Database *string         `json:"database,omitempty"`
	Hosts    *[]string       `json:"hosts,omitempty"`
	Socket   *string         `json:"socket,omitempty"`
	Username *string         `json:"username,omitempty"`
	Password *PasswordSource `json:"password,omitempty"`
}

type privatePostgreSQLOptions struct {
	RequirePrimary *bool `json:"require_primary,omitempty"`
}

type privateMongoDBOptions struct {
	ReplicaSet *string `json:"replica_set,omitempty"`
	ConfigFile *string `json:"config_file,omitempty"`
}

func bindPrivateConfig(profile *Profile, path string) error {
	var configured privateConfig
	if err := decodePrivateStrict(path, "private configuration", &configured); err != nil {
		return err
	}
	base := filepath.Dir(path)
	if configured.Repository != nil {
		profile.Repository = *configured.Repository
	}
	if configured.Credentials != nil {
		if configured.Credentials.Environment != nil {
			profile.Credentials.Environment = cloneEnvironment(*configured.Credentials.Environment)
		}
		if configured.Credentials.Password != nil {
			profile.Credentials.Password = *configured.Credentials.Password
		}
	}
	if err := validateRepositoryCredentials(&profile.Credentials, base, "private credentials"); err != nil {
		return err
	}
	if configured.Databases == nil {
		return nil
	}
	for _, name := range sortedMapKeys(configured.Databases.SQLite) {
		value := configured.Databases.SQLite[name]
		if err := validatePrivateDatabaseName(name); err != nil {
			return err
		}
		if err := expandPrivatePath(&value.Path, base, "path", name); err != nil {
			return err
		}
		index, err := matchPrivateDatabase("sqlite", name, len(profile.SQLiteDatabases), func(i int) string { return profile.SQLiteDatabases[i].Name })
		if err != nil {
			return err
		}
		if value.Path != nil {
			profile.SQLiteDatabases[index].Path = *value.Path
		}
	}
	for _, name := range sortedMapKeys(configured.Databases.PostgreSQL) {
		value := configured.Databases.PostgreSQL[name]
		if err := preparePrivateConnection(name, value.Connection, base); err != nil {
			return err
		}
		index, err := matchPrivateDatabase("postgresql", name, len(profile.PostgreSQLDatabases), func(i int) string { return profile.PostgreSQLDatabases[i].Name })
		if err != nil {
			return err
		}
		db := &profile.PostgreSQLDatabases[index]
		db.Connection = mergeConnection(db.Connection, value.Connection)
		db.Options = mergePostgreSQLOptions(db.Options, value.Options)
	}
	for _, name := range sortedMapKeys(configured.Databases.MongoDB) {
		value := configured.Databases.MongoDB[name]
		if err := preparePrivateConnection(name, value.Connection, base); err != nil {
			return err
		}
		if value.Options != nil && value.Options.ConfigFile != nil && *value.Options.ConfigFile != "" {
			expanded, err := expandPath(*value.Options.ConfigFile, base)
			if err != nil {
				return fmt.Errorf("invalid private config_file for %s: %w", name, err)
			}
			value.Options.ConfigFile = &expanded
		}
		index, err := matchPrivateDatabase("mongodb", name, len(profile.MongoDBDatabases), func(i int) string { return profile.MongoDBDatabases[i].Name })
		if err != nil {
			return err
		}
		db := &profile.MongoDBDatabases[index]
		db.Connection = mergeConnection(db.Connection, value.Connection)
		db.Options = mergeMongoDBOptions(db.Options, value.Options)
	}
	for _, name := range sortedMapKeys(configured.Databases.MySQL) {
		value := configured.Databases.MySQL[name]
		if err := preparePrivateConnection(name, value.Connection, base); err != nil {
			return err
		}
		index, err := matchPrivateDatabase("mysql", name, len(profile.MySQLDatabases), func(i int) string { return profile.MySQLDatabases[i].Name })
		if err != nil {
			return err
		}
		db := &profile.MySQLDatabases[index]
		db.Connection = mergeConnection(db.Connection, value.Connection)
	}
	for _, name := range sortedMapKeys(configured.Databases.SQLServer) {
		value := configured.Databases.SQLServer[name]
		if err := preparePrivateConnection(name, value.Connection, base); err != nil {
			return err
		}
		if err := expandPrivatePath(&value.BackupDirectory, base, "backup_directory", name); err != nil {
			return err
		}
		index, err := matchPrivateDatabase("sqlserver", name, len(profile.SQLServerDatabases), func(i int) string { return profile.SQLServerDatabases[i].Name })
		if err != nil {
			return err
		}
		db := &profile.SQLServerDatabases[index]
		db.Connection = mergeConnection(db.Connection, value.Connection)
		if value.BackupDirectory != nil {
			db.BackupDirectory = *value.BackupDirectory
		}
	}
	return nil
}

func validatePrivateDatabaseName(name string) error {
	if !isPortableName(name) {
		return fmt.Errorf("invalid private database name: %s", name)
	}
	return nil
}

func preparePrivateConnection(name string, connection *privateConnection, base string) error {
	if err := validatePrivateDatabaseName(name); err != nil {
		return err
	}
	if connection != nil && connection.Password != nil {
		if err := validatePasswordSource("database password", connection.Password, base, true); err != nil {
			return err
		}
	}
	return nil
}

func expandPrivatePath(target **string, base, field, name string) error {
	if *target == nil {
		return nil
	}
	expanded, err := expandPath(**target, base)
	if err != nil {
		return fmt.Errorf("invalid private %s for %s: %w", field, name, err)
	}
	*target = &expanded
	return nil
}

func matchPrivateDatabase(provider, name string, count int, configuredName func(int) string) (int, error) {
	for i := range count {
		if strings.EqualFold(configuredName(i), name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("private configuration references unknown %s database %s", provider, name)
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func cloneEnvironment(environment map[string]string) map[string]string {
	result := make(map[string]string, len(environment))
	for key, value := range environment {
		result[key] = value
	}
	return result
}

func mergeConnection(base *DatabaseConnection, override *privateConnection) *DatabaseConnection {
	if base == nil && override == nil {
		return nil
	}
	result := DatabaseConnection{}
	if base != nil {
		result = *base
		result.Hosts = append([]string(nil), base.Hosts...)
	}
	if override == nil {
		return &result
	}
	if override.Database != nil {
		result.Database = *override.Database
	}
	if override.Hosts != nil {
		result.Hosts = append([]string(nil), (*override.Hosts)...)
	}
	if override.Socket != nil {
		result.Socket = *override.Socket
	}
	if override.Username != nil {
		result.Username = *override.Username
	}
	if override.Password != nil {
		password := *override.Password
		result.Password = &password
	}
	return &result
}

func mergePostgreSQLOptions(base *PostgreSQLOptions, override *privatePostgreSQLOptions) *PostgreSQLOptions {
	if base == nil && (override == nil || override.RequirePrimary == nil) {
		return nil
	}
	result := PostgreSQLOptions{}
	if base != nil {
		result = *base
	}
	if override != nil && override.RequirePrimary != nil {
		result.RequirePrimary = *override.RequirePrimary
	}
	return &result
}

func mergeMongoDBOptions(base *MongoDBOptions, override *privateMongoDBOptions) *MongoDBOptions {
	if base == nil && override == nil {
		return nil
	}
	result := MongoDBOptions{}
	if base != nil {
		result = *base
	}
	if override != nil {
		if override.ReplicaSet != nil {
			result.ReplicaSet = *override.ReplicaSet
		}
		if override.ConfigFile != nil {
			result.ConfigFile = *override.ConfigFile
		}
	}
	return &result
}

func validatePasswordSource(label string, source *PasswordSource, base string, required bool) error {
	configured := 0
	if source.Value != "" {
		configured++
	}
	if source.File != "" {
		configured++
	}
	if source.Command != nil {
		configured++
	}
	if configured == 0 && !required {
		return nil
	}
	if configured != 1 {
		return fmt.Errorf("set exactly one source for %s", label)
	}
	if strings.ContainsRune(source.Value, 0) {
		return fmt.Errorf("%s value must not contain NUL bytes", label)
	}
	if len(source.Value) > secretvalue.MaximumBytes {
		return fmt.Errorf("%s value exceeds 1 MiB", label)
	}
	if source.File != "" {
		path, err := expandPath(source.File, base)
		if err != nil {
			return err
		}
		if err := ensurePrivateFile(path, label); err != nil {
			return err
		}
		source.File = path
	}
	if source.Command != nil {
		if len(source.Command) == 0 {
			return fmt.Errorf("%s command must not be empty", label)
		}
		for _, part := range source.Command {
			if part == "" || strings.ContainsRune(part, 0) {
				return fmt.Errorf("%s command contains an invalid argument", label)
			}
		}
	}
	return nil
}

func normalizeConnections(p *Profile, base string) error {
	if p.Credentials.DatabaseCredentials == nil {
		p.Credentials.DatabaseCredentials = make(map[string]DatabaseCredential)
	}
	for i := range p.PostgreSQLDatabases {
		db := &p.PostgreSQLDatabases[i]
		if db.Connection == nil {
			continue
		}
		if db.Database != "" || db.Host != "" || db.Port != 0 || db.Username != "" {
			return fmt.Errorf("PostgreSQL database %s mixes connection with legacy fields", db.Name)
		}
		if err := validateConnection("PostgreSQL", db.Name, db.Connection, true); err != nil {
			return err
		}
		db.Database, db.Hosts, db.Username = db.Connection.Database, db.Connection.Hosts, db.Connection.Username
		if db.Connection.Socket != "" {
			db.Host = db.Connection.Socket
		}
		if err := normalizeDatabasePassword(p, db.Name, db.Connection, base); err != nil {
			return err
		}
	}
	for i := range p.MongoDBDatabases {
		db := &p.MongoDBDatabases[i]
		if db.Options != nil {
			if db.ConfigFile != "" {
				return fmt.Errorf("MongoDB database %s mixes options with legacy config_file", db.Name)
			}
			if strings.TrimSpace(db.Options.ReplicaSet) != db.Options.ReplicaSet || strings.ContainsAny(db.Options.ReplicaSet, "\x00/,") {
				return fmt.Errorf("invalid MongoDB replica_set for %s", db.Name)
			}
			if db.Options.ReplicaSet != "" && (db.Connection == nil || len(db.Connection.Hosts) == 0) {
				return fmt.Errorf("MongoDB replica_set for %s requires hosts", db.Name)
			}
			db.ConfigFile = db.Options.ConfigFile
		}
		if db.Connection == nil {
			continue
		}
		if db.Database != "" || db.Host != "" || db.Port != 0 || db.Username != "" {
			return fmt.Errorf("MongoDB database %s mixes connection with legacy fields", db.Name)
		}
		if err := validateConnection("MongoDB", db.Name, db.Connection, true); err != nil {
			return err
		}
		if db.Connection.Password != nil {
			return fmt.Errorf("MongoDB database %s does not support connection.password; use config_file", db.Name)
		}
		db.Database, db.Hosts, db.Username = db.Connection.Database, db.Connection.Hosts, db.Connection.Username
		if db.Connection.Socket != "" {
			db.Host = db.Connection.Socket
		}
	}
	for i := range p.MySQLDatabases {
		db := &p.MySQLDatabases[i]
		if db.Connection == nil {
			continue
		}
		if db.Database != "" || db.Host != "" || db.Port != 0 || db.Socket != "" || db.Username != "" {
			return fmt.Errorf("MySQL database %s mixes connection with legacy fields", db.Name)
		}
		if err := validateConnection("MySQL", db.Name, db.Connection, false); err != nil {
			return err
		}
		db.Database, db.Socket, db.Username = db.Connection.Database, db.Connection.Socket, db.Connection.Username
		if len(db.Connection.Hosts) == 1 {
			db.Host, db.Port = splitConfiguredHost(db.Connection.Hosts[0])
		}
		if err := normalizeDatabasePassword(p, db.Name, db.Connection, base); err != nil {
			return err
		}
	}
	for i := range p.SQLServerDatabases {
		db := &p.SQLServerDatabases[i]
		if db.Connection == nil {
			continue
		}
		if db.Database != "" || db.Host != "" || db.Port != 0 || db.Username != "" {
			return fmt.Errorf("SQL Server database %s mixes connection with legacy fields", db.Name)
		}
		if err := validateConnection("SQL Server", db.Name, db.Connection, false); err != nil {
			return err
		}
		if db.Connection.Socket != "" {
			return fmt.Errorf("SQL Server database %s does not support sockets", db.Name)
		}
		db.Database, db.Username = db.Connection.Database, db.Connection.Username
		if len(db.Connection.Hosts) == 1 {
			db.Host, db.Port = splitConfiguredHost(db.Connection.Hosts[0])
		}
		if err := normalizeDatabasePassword(p, db.Name, db.Connection, base); err != nil {
			return err
		}
	}
	return nil
}

func normalizeDatabasePassword(p *Profile, name string, connection *DatabaseConnection, base string) error {
	if connection == nil || connection.Password == nil {
		return nil
	}
	if err := validatePasswordSource("database password", connection.Password, base, true); err != nil {
		return err
	}
	key := name
	credential := DatabaseCredential{}
	for configuredName, configured := range p.Credentials.DatabaseCredentials {
		if strings.EqualFold(configuredName, name) {
			key, credential = configuredName, configured
			break
		}
	}
	if credential.Password.Configured() {
		return fmt.Errorf("database %s configures password in both connection and credentials", name)
	}
	credential.Password = *connection.Password
	p.Credentials.DatabaseCredentials[key] = credential
	connection.Password = nil
	return nil
}

func splitConfiguredHost(value string) (string, int) {
	if !strings.Contains(value, ":") {
		return value, 0
	}
	host, rawPort, _ := net.SplitHostPort(value)
	port, _ := strconv.Atoi(rawPort)
	return host, port
}

func validateConnection(provider, name string, c *DatabaseConnection, multiple bool) error {
	if strings.TrimSpace(c.Username) != c.Username || strings.HasPrefix(c.Username, "-") || strings.ContainsRune(c.Username, 0) {
		return fmt.Errorf("invalid %s username for %s", provider, name)
	}
	if len(c.Hosts) > 0 && c.Socket != "" {
		return fmt.Errorf("%s database %s connection sets both hosts and socket", provider, name)
	}
	if !multiple && len(c.Hosts) > 1 {
		return fmt.Errorf("%s database %s supports at most one host", provider, name)
	}
	seen := make(map[string]struct{}, len(c.Hosts))
	for _, host := range c.Hosts {
		if host == "" || strings.TrimSpace(host) != host || strings.HasPrefix(host, "-") || strings.ContainsAny(host, "\x00/?#@,") {
			return fmt.Errorf("invalid %s host for %s", provider, name)
		}
		if strings.Contains(host, ":") {
			hostname, rawPort, err := net.SplitHostPort(host)
			if err != nil || hostname == "" {
				return fmt.Errorf("invalid %s host for %s", provider, name)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("invalid %s port for %s", provider, name)
			}
		}
		key := strings.ToLower(host)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate %s host for %s", provider, name)
		}
		seen[key] = struct{}{}
	}
	return nil
}
