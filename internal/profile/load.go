package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"resticctl/internal/cronexpr"
)

func Load(configDir, name string) (Profile, error) {
	if err := ValidateName(name); err != nil {
		return Profile{}, err
	}
	configured, err := resolve(configDir, name, nil)
	if err != nil {
		return Profile{}, err
	}
	backupProfile := configured.profile(name)
	profilePath := filepath.Join(configDir, name+".json")
	base := filepath.Dir(profilePath)
	if backupProfile.PrivateFile != "" {
		if backupProfile.CredentialsFile != "" {
			return Profile{}, errors.New("private_file must not be combined with credentials_file")
		}
		if err := validateRepositoryCredentialFields(&backupProfile.Credentials, base, "credentials", false); err != nil {
			return Profile{}, err
		}
		privatePath, expandErr := expandPath(backupProfile.PrivateFile, base)
		if expandErr != nil {
			return Profile{}, fmt.Errorf("invalid private_file: %w", expandErr)
		}
		backupProfile.PrivateFile = privatePath
		if err := bindPrivateConfig(&backupProfile, privatePath); err != nil {
			return Profile{}, err
		}
	} else if backupProfile.CredentialsFile != "" {
		if backupProfile.Credentials.Password.Configured() || backupProfile.Credentials.Environment != nil {
			return Profile{}, errors.New("credentials must not be combined with credentials_file")
		}
		credentialsPath, expandErr := expandPath(backupProfile.CredentialsFile, base)
		if expandErr != nil {
			return Profile{}, fmt.Errorf("invalid credentials_file: %w", expandErr)
		}
		backupProfile.CredentialsFile = credentialsPath
		credentials, loadErr := loadCredentials(credentialsPath)
		if loadErr != nil {
			return Profile{}, loadErr
		}
		backupProfile.Credentials = credentials
	} else if err := validateRepositoryCredentials(&backupProfile.Credentials, base, "credentials"); err != nil {
		return Profile{}, errors.New("set private_file, credentials_file, or valid inline credentials: " + err.Error())
	}
	if backupProfile.Repository == "" {
		return Profile{}, errors.New("repository must be a non-empty string")
	}
	if strings.ContainsRune(backupProfile.Repository, 0) {
		return Profile{}, errors.New("repository must not contain NUL bytes")
	}

	for index, path := range backupProfile.BackupPaths {
		if path == "" {
			return Profile{}, errors.New("backup_paths must not contain empty strings")
		}
		expanded, expandErr := expandPath(path, base)
		if expandErr != nil {
			return Profile{}, fmt.Errorf("invalid backup_paths entry: %w", expandErr)
		}
		backupProfile.BackupPaths[index] = expanded
	}
	if err := normalizeConnections(&backupProfile, base); err != nil {
		return Profile{}, err
	}

	names := make(map[string]struct{})
	for index := range backupProfile.SQLiteDatabases {
		database := &backupProfile.SQLiteDatabases[index]
		if !isPortableName(database.Name) {
			return Profile{}, fmt.Errorf("invalid SQLite backup name: %s", database.Name)
		}
		normalized := strings.ToLower(database.Name)
		if _, exists := names[normalized]; exists {
			return Profile{}, fmt.Errorf("duplicate SQLite backup name: %s", database.Name)
		}
		names[normalized] = struct{}{}
		if database.Path == "" {
			return Profile{}, fmt.Errorf("SQLite database path is missing: %s", database.Name)
		}
		database.Path, err = expandPath(database.Path, base)
		if err != nil {
			return Profile{}, fmt.Errorf("invalid SQLite database path: %w", err)
		}
	}
	if err := validateExternalDatabases(&backupProfile, base); err != nil {
		return Profile{}, err
	}
	if backupProfile.DatabaseConcurrency <= 0 {
		return Profile{}, errors.New("databases.concurrency must be a positive integer (legacy: database_concurrency)")
	}
	if err := validateDatabaseEnvironmentNames(&backupProfile); err != nil {
		return Profile{}, err
	}

	argumentLists := []struct {
		name   string
		values []string
	}{
		{"restic_args", backupProfile.ResticArgs},
		{"backup_args", backupProfile.BackupArgs},
		{"tags", backupProfile.Tags},
		{"forget_args", backupProfile.ForgetArgs},
		{"check_args", backupProfile.CheckArgs},
	}
	for _, list := range argumentLists {
		for _, value := range list.values {
			if value == "" || strings.ContainsRune(value, 0) {
				return Profile{}, fmt.Errorf("%s must not contain empty strings or NUL bytes", list.name)
			}
			if IsReservedOption(value) {
				return Profile{}, fmt.Errorf("%s must not override repository or password options: %s", list.name, value)
			}
			if list.name == "backup_args" && IsDryRunOption(value) {
				return Profile{}, fmt.Errorf("backup_args must not set workflow-owned dry-run option: %s", value)
			}
		}
	}
	commandNames := make([]string, 0, len(backupProfile.Commands))
	for name := range backupProfile.Commands {
		commandNames = append(commandNames, name)
	}
	sort.Strings(commandNames)
	for _, name := range commandNames {
		command := backupProfile.Commands[name]
		if !IsSupportedResticCommand(name) {
			return Profile{}, fmt.Errorf("commands contains unsupported Restic command %q", name)
		}
		for _, value := range command.Args {
			if value == "" || strings.ContainsRune(value, 0) {
				return Profile{}, fmt.Errorf("commands.%s.args must not contain empty strings or NUL bytes", name)
			}
			if IsReservedOption(value) {
				return Profile{}, fmt.Errorf("commands.%s.args must not override repository or password options: %s", name, value)
			}
			if name == "backup" && IsDryRunOption(value) {
				return Profile{}, fmt.Errorf("commands.backup.args must not set workflow-owned dry-run option: %s", value)
			}
		}
	}
	if (backupProfile.PruneBefore || backupProfile.PruneAfter) && len(backupProfile.ForgetArgs) == 0 {
		return Profile{}, errors.New("backup pruning requires non-empty forget_args")
	}
	for _, hooks := range []struct {
		name   string
		values []Hook
	}{
		{"run_before", backupProfile.RunBefore},
		{"run_after", backupProfile.RunAfter},
		{"run_after_fail", backupProfile.RunAfterFail},
		{"run_finally", backupProfile.RunFinally},
	} {
		for index, hook := range hooks.values {
			if len(hook.Command) == 0 {
				return Profile{}, fmt.Errorf("%s[%d].command must contain at least one argument", hooks.name, index)
			}
			for _, part := range hook.Command {
				if part == "" || strings.ContainsRune(part, 0) {
					return Profile{}, fmt.Errorf("%s[%d].command must not contain empty arguments or NUL bytes", hooks.name, index)
				}
			}
			if hook.Timeout != "" {
				timeout, err := time.ParseDuration(hook.Timeout)
				if err != nil || timeout <= 0 {
					return Profile{}, fmt.Errorf("%s[%d].timeout must be a positive duration", hooks.name, index)
				}
			}
		}
	}
	if backupProfile.Schedule != nil {
		schedule := backupProfile.Schedule
		if schedule.Backend == "" {
			schedule.Backend = "auto"
		}
		if !validScheduleBackend(schedule.Backend) {
			return Profile{}, fmt.Errorf("schedule.backend is unsupported: %s", schedule.Backend)
		}
		normalized, err := cronexpr.Normalize(schedule.Cron)
		if err != nil {
			return Profile{}, fmt.Errorf("invalid schedule.cron: %w", err)
		}
		schedule.Cron = normalized
	}
	if backupProfile.Forget != nil {
		forget := backupProfile.Forget
		if len(backupProfile.ForgetArgs) == 0 {
			return Profile{}, errors.New("forget schedule requires non-empty forget_args")
		}
		if forget.Backend == "" {
			forget.Backend = "auto"
		}
		if !validScheduleBackend(forget.Backend) {
			return Profile{}, fmt.Errorf("forget.backend is unsupported: %s", forget.Backend)
		}
		if forget.Cron != "" && forget.Schedule != "" {
			return Profile{}, errors.New("forget must not set both cron and deprecated schedule")
		}
		expression := forget.Cron
		if expression == "" {
			expression = forget.Schedule
		}
		normalized, err := cronexpr.Normalize(expression)
		if err != nil {
			return Profile{}, fmt.Errorf("invalid forget.cron: %w", err)
		}
		forget.Cron = normalized
		forget.Schedule = ""
	}
	if err := validateMonitoring(&backupProfile, base); err != nil {
		return Profile{}, err
	}
	return backupProfile, nil
}

func validScheduleBackend(value string) bool {
	return value == "auto" || value == "cron" || value == "launchd" || value == "systemd" || value == "windows"
}

// profileConfig keeps optional values distinct from their runtime defaults.
type profileConfig struct {
	Parent              string                   `json:"parent,omitempty"`
	Repository          *string                  `json:"repository"`
	CredentialsFile     *string                  `json:"credentials_file"`
	PrivateFile         *string                  `json:"private_file,omitempty"`
	Credentials         *RepositoryCredentials   `json:"credentials,omitempty"`
	BackupPaths         []string                 `json:"backup_paths"`
	SQLiteDatabases     []SQLiteDatabase         `json:"sqlite_databases"`
	PostgreSQLDatabases []PostgreSQLDatabase     `json:"postgresql_databases,omitempty"`
	MongoDBDatabases    []MongoDBDatabase        `json:"mongodb_databases,omitempty"`
	MySQLDatabases      []MySQLDatabase          `json:"mysql_databases,omitempty"`
	SQLServerDatabases  []SQLServerDatabase      `json:"sqlserver_databases,omitempty"`
	DatabaseConcurrency *int                     `json:"database_concurrency,omitempty"`
	Databases           *databaseConfig          `json:"databases,omitempty"`
	ResticArgs          []string                 `json:"restic_args"`
	Commands            map[string]ResticCommand `json:"commands,omitempty"`
	BackupArgs          []string                 `json:"backup_args"`
	Tags                []string                 `json:"tags"`
	ForgetArgs          []string                 `json:"forget_args"`
	CheckArgs           []string                 `json:"check_args"`
	CheckBefore         *bool                    `json:"check_before"`
	CheckAfter          *bool                    `json:"check_after"`
	PruneBefore         *bool                    `json:"prune_before"`
	PruneAfter          *bool                    `json:"prune_after"`
	RunBefore           []Hook                   `json:"run_before"`
	RunAfter            []Hook                   `json:"run_after"`
	RunAfterFail        []Hook                   `json:"run_after_fail"`
	RunFinally          []Hook                   `json:"run_finally"`
	Schedule            *Schedule                `json:"schedule,omitempty"`
	Forget              *ForgetSchedule          `json:"forget,omitempty"`
	Monitoring          *Monitoring              `json:"monitoring,omitempty"`
}

type databaseConfig struct {
	Concurrency *int                          `json:"concurrency,omitempty"`
	SQLite      map[string]SQLiteDatabase     `json:"sqlite,omitempty"`
	PostgreSQL  map[string]PostgreSQLDatabase `json:"postgresql,omitempty"`
	MongoDB     map[string]MongoDBDatabase    `json:"mongodb,omitempty"`
	MySQL       map[string]MySQLDatabase      `json:"mysql,omitempty"`
	SQLServer   map[string]SQLServerDatabase  `json:"sqlserver,omitempty"`
}

func resolve(configDir, name string, chain []string) (profileConfig, error) {
	document, err := resolveDocument(configDir, name, chain)
	if err != nil {
		return profileConfig{}, err
	}
	data, err := json.Marshal(document)
	if err != nil {
		return profileConfig{}, fmt.Errorf("merge profile %s: %w", name, err)
	}
	var configured profileConfig
	if err := decodeStrictJSON(data, &configured); err != nil {
		return profileConfig{}, fmt.Errorf("merge profile %s: %w", name, err)
	}
	if err := configured.normalizeDatabases(); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	return configured, nil
}

func resolveDocument(configDir, name string, chain []string) (map[string]json.RawMessage, error) {
	if err := ValidateName(name); err != nil {
		return nil, fmt.Errorf("invalid parent profile %q: %w", name, err)
	}
	for _, ancestor := range chain {
		if strings.EqualFold(ancestor, name) {
			return nil, fmt.Errorf("profile inheritance cycle: %s", strings.Join(append(chain, name), " -> "))
		}
	}
	path := filepath.Join(configDir, name+".json")
	data, info, err := readStrictJSONFile(path, "profile")
	if err != nil {
		if len(chain) > 0 {
			return nil, fmt.Errorf("cannot load parent profile %q: %w", name, err)
		}
		return nil, err
	}
	var child profileConfig
	if err := decodeStrictJSON(data, &child); err != nil {
		return nil, fmt.Errorf("cannot load profile %s: %w", path, err)
	}
	validated := child
	if err := validated.normalizeDatabases(); err != nil {
		return nil, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	if validated.containsInlineSecrets() {
		if err := ensureFileSecurity(info, path, "profile containing secrets"); err != nil {
			return nil, err
		}
	}
	if err := validateConfiguredDatabases(validated.SQLiteDatabases); err != nil {
		return nil, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	if err := validateConfiguredNames(validated.PostgreSQLDatabases, func(v PostgreSQLDatabase) string { return v.Name }); err != nil {
		return nil, fmt.Errorf("invalid profile %s PostgreSQL databases: %w", name, err)
	}
	if err := validateConfiguredNames(validated.MongoDBDatabases, func(v MongoDBDatabase) string { return v.Name }); err != nil {
		return nil, fmt.Errorf("invalid profile %s MongoDB databases: %w", name, err)
	}
	if err := validateConfiguredNames(validated.MySQLDatabases, func(v MySQLDatabase) string { return v.Name }); err != nil {
		return nil, fmt.Errorf("invalid profile %s MySQL databases: %w", name, err)
	}
	if err := validateConfiguredNames(validated.SQLServerDatabases, func(v SQLServerDatabase) string { return v.Name }); err != nil {
		return nil, fmt.Errorf("invalid profile %s SQL Server databases: %w", name, err)
	}
	var childDocument map[string]json.RawMessage
	if err := decodeStrictJSON(data, &childDocument); err != nil {
		return nil, fmt.Errorf("cannot load profile %s: %w", path, err)
	}
	if child.Parent == "" {
		return childDocument, nil
	}
	parent, err := resolveDocument(configDir, child.Parent, append(chain, name))
	if err != nil {
		return nil, err
	}
	// Credentials and private-file selection always belong to the requested
	// profile and must never flow down from a parent.
	deleteJSONField(parent, "credentials")
	deleteJSONField(parent, "credentials_file")
	deleteJSONField(parent, "private_file")
	merged, err := mergeJSONObjects(parent, childDocument)
	if err != nil {
		return nil, fmt.Errorf("merge profile %s: %w", name, err)
	}
	return merged, nil
}

func mergeJSONObjects(parent, child map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	return mergeJSONObjectsAtPath(parent, child, nil)
}

func mergeJSONObjectsAtPath(parent, child map[string]json.RawMessage, path []string) (map[string]json.RawMessage, error) {
	if len(path) > 100 {
		return nil, errors.New("profile objects are nested too deeply")
	}
	result := make(map[string]json.RawMessage, len(parent)+len(child))
	for key, value := range parent {
		result[key] = append(json.RawMessage(nil), value...)
	}
	for key, childValue := range child {
		parentKey := matchingJSONKey(result, key)
		if strings.EqualFold(key, "password") &&
			(len(path) == 1 && strings.EqualFold(path[0], "credentials") ||
				len(path) == 4 && strings.EqualFold(path[3], "connection")) {
			delete(result, parentKey)
			result[key] = append(json.RawMessage(nil), childValue...)
			continue
		}
		var childObject map[string]json.RawMessage
		var parentObject map[string]json.RawMessage
		if json.Unmarshal(childValue, &childObject) == nil && childObject != nil &&
			json.Unmarshal(result[parentKey], &parentObject) == nil && parentObject != nil {
			mergedObject, err := mergeJSONObjectsAtPath(parentObject, childObject, append(path, key))
			if err != nil {
				return nil, err
			}
			merged, err := json.Marshal(mergedObject)
			if err != nil {
				return nil, err
			}
			delete(result, parentKey)
			result[key] = merged
			continue
		}
		delete(result, parentKey)
		result[key] = append(json.RawMessage(nil), childValue...)
	}
	return result, nil
}

func deleteJSONField(object map[string]json.RawMessage, name string) {
	delete(object, matchingJSONKey(object, name))
}

func matchingJSONKey(object map[string]json.RawMessage, name string) string {
	for key := range object {
		if strings.EqualFold(key, name) {
			return key
		}
	}
	return name
}

func (configured profileConfig) containsInlineSecrets() bool {
	if configured.Repository != nil && redactRepository(*configured.Repository) != *configured.Repository {
		return true
	}
	if configured.Credentials != nil && (configured.Credentials.Environment != nil || configured.Credentials.Password.Configured()) {
		return true
	}
	if configured.Monitoring != nil && monitoringContainsSecrets(*configured.Monitoring) {
		return true
	}
	for _, database := range configured.PostgreSQLDatabases {
		if connectionHasPassword(database.Connection) {
			return true
		}
	}
	for _, database := range configured.MongoDBDatabases {
		if connectionHasPassword(database.Connection) {
			return true
		}
	}
	for _, database := range configured.MySQLDatabases {
		if connectionHasPassword(database.Connection) {
			return true
		}
	}
	for _, database := range configured.SQLServerDatabases {
		if connectionHasPassword(database.Connection) {
			return true
		}
	}
	return false
}

func monitoringContainsSecrets(monitoring Monitoring) bool {
	if monitoring.Pushgateway != nil &&
		(endpointContainsSecrets(monitoring.Pushgateway.URL) || len(monitoring.Pushgateway.Headers) > 0) {
		return true
	}
	for _, hook := range monitoring.HTTP {
		if endpointContainsSecrets(hook.URL) || len(hook.Headers) > 0 || hook.Body != "" || hook.BodyTemplate != "" {
			return true
		}
	}
	return false
}

func endpointContainsSecrets(endpoint string) bool {
	return endpoint != "" && redactEndpoint(endpoint) != endpoint
}

func connectionHasPassword(connection *DatabaseConnection) bool {
	return connection != nil && connection.Password != nil && connection.Password.Configured()
}

func (configured *profileConfig) normalizeDatabases() error {
	if configured.Databases == nil {
		return nil
	}
	if configured.SQLiteDatabases != nil || configured.PostgreSQLDatabases != nil || configured.MongoDBDatabases != nil ||
		configured.MySQLDatabases != nil || configured.SQLServerDatabases != nil || configured.DatabaseConcurrency != nil {
		return errors.New("databases must not be combined with legacy top-level database fields")
	}
	configured.DatabaseConcurrency = configured.Databases.Concurrency
	var err error
	if configured.SQLiteDatabases, err = namedDatabaseValues(configured.Databases.SQLite, func(value SQLiteDatabase) string { return value.Name }, func(value *SQLiteDatabase, name string) { value.Name = name }); err != nil {
		return fmt.Errorf("invalid databases.sqlite: %w", err)
	}
	if configured.PostgreSQLDatabases, err = namedDatabaseValues(configured.Databases.PostgreSQL, func(value PostgreSQLDatabase) string { return value.Name }, func(value *PostgreSQLDatabase, name string) { value.Name = name }); err != nil {
		return fmt.Errorf("invalid databases.postgresql: %w", err)
	}
	if configured.MongoDBDatabases, err = namedDatabaseValues(configured.Databases.MongoDB, func(value MongoDBDatabase) string { return value.Name }, func(value *MongoDBDatabase, name string) { value.Name = name }); err != nil {
		return fmt.Errorf("invalid databases.mongodb: %w", err)
	}
	if configured.MySQLDatabases, err = namedDatabaseValues(configured.Databases.MySQL, func(value MySQLDatabase) string { return value.Name }, func(value *MySQLDatabase, name string) { value.Name = name }); err != nil {
		return fmt.Errorf("invalid databases.mysql: %w", err)
	}
	if configured.SQLServerDatabases, err = namedDatabaseValues(configured.Databases.SQLServer, func(value SQLServerDatabase) string { return value.Name }, func(value *SQLServerDatabase, name string) { value.Name = name }); err != nil {
		return fmt.Errorf("invalid databases.sqlserver: %w", err)
	}
	configured.Databases = nil
	return nil
}

func namedDatabaseValues[T any](values map[string]T, configuredName func(T) string, setName func(*T, string)) ([]T, error) {
	if values == nil {
		return nil, nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if !isPortableName(name) {
			return nil, fmt.Errorf("invalid backup name %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]T, 0, len(names))
	for _, name := range names {
		value := values[name]
		if configuredName(value) != "" {
			return nil, fmt.Errorf("database %q must not contain a name field", name)
		}
		setName(&value, name)
		result = append(result, value)
	}
	return result, nil
}

func validateConfiguredNames[T any](values []T, name func(T) string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.ToLower(name(value))
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate backup name: %s", name(value))
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateConfiguredDatabases(databases []SQLiteDatabase) error {
	names := make(map[string]struct{}, len(databases))
	for _, database := range databases {
		normalized := strings.ToLower(database.Name)
		if _, exists := names[normalized]; exists {
			return fmt.Errorf("duplicate SQLite backup name: %s", database.Name)
		}
		names[normalized] = struct{}{}
	}
	return nil
}

func validateExternalDatabases(p *Profile, base string) error {
	seen := make(map[string]struct{})
	checkName := func(backend, name string) error {
		if !isPortableName(name) {
			return fmt.Errorf("invalid %s backup name: %s", backend, name)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate database backup name: %s", name)
		}
		seen[key] = struct{}{}
		return nil
	}
	for _, db := range p.SQLiteDatabases {
		seen[strings.ToLower(db.Name)] = struct{}{}
	}
	for i := range p.PostgreSQLDatabases {
		db := &p.PostgreSQLDatabases[i]
		if err := checkName("PostgreSQL", db.Name); err != nil {
			return err
		}
		if db.Database == "" {
			return fmt.Errorf("PostgreSQL database is missing: %s", db.Name)
		}
		if strings.Contains(db.Database, "://") || strings.Contains(strings.ToLower(db.Database), "password=") {
			return fmt.Errorf("PostgreSQL database for %s must be a name, not a credential-bearing connection string", db.Name)
		}
		if strings.HasPrefix(db.Database, "-") {
			return fmt.Errorf("PostgreSQL database for %s must not start with a hyphen", db.Name)
		}
		if db.Port < 0 || db.Port > 65535 {
			return fmt.Errorf("invalid PostgreSQL port for %s", db.Name)
		}
		if db.Executable == "" {
			db.Executable = "pg_dump"
		}
		if db.GlobalsExecutable == "" {
			db.GlobalsExecutable = "pg_dumpall"
		}
		if hasNUL(db.Database, db.Host, db.Username, db.Executable, db.GlobalsExecutable) {
			return fmt.Errorf("PostgreSQL configuration for %s must not contain NUL bytes", db.Name)
		}
		if err := validateNames("PostgreSQL table pattern", db.Name, db.TablePatterns); err != nil {
			return err
		}
		if len(db.TablePatterns) > 0 && containsAnyOption(db.Args,
			"--exclude-table", "--exclude-table-and-children", "--exclude-table-data", "--exclude-table-data-and-children",
			"--schema", "-n", "--exclude-schema", "-N", "--table-and-children") {
			return fmt.Errorf("PostgreSQL table_patterns for %s cannot be combined with other selection options", db.Name)
		}
		if err := validateDatabaseArgs("PostgreSQL", db.Args, "--file", "-f", "--password", "--dbname", "-d", "--host", "-h", "--port", "-p", "--username", "-U", "--table", "-t"); err != nil {
			return err
		}
	}
	for i := range p.MongoDBDatabases {
		db := &p.MongoDBDatabases[i]
		if err := checkName("MongoDB", db.Name); err != nil {
			return err
		}
		if db.Port < 0 || db.Port > 65535 {
			return fmt.Errorf("invalid MongoDB port for %s", db.Name)
		}
		if db.Executable == "" {
			db.Executable = "mongodump"
		}
		if hasNUL(db.Database, db.Host, db.Username, db.Executable, db.ConfigFile) {
			return fmt.Errorf("MongoDB configuration for %s must not contain NUL bytes", db.Name)
		}
		if db.Collection != "" && db.Database == "" {
			return fmt.Errorf("MongoDB collection requires a database: %s", db.Name)
		}
		if len(db.ExcludeCollections) > 0 && db.Database == "" {
			return fmt.Errorf("MongoDB exclude_collections require a database: %s", db.Name)
		}
		if db.Collection != "" && len(db.ExcludeCollections) > 0 {
			return fmt.Errorf("MongoDB database %s must not set both collection and exclude_collections", db.Name)
		}
		if strings.ContainsRune(db.Collection, 0) {
			return fmt.Errorf("invalid MongoDB collection for %s", db.Name)
		}
		if err := validateNames("MongoDB excluded collection", db.Name, db.ExcludeCollections); err != nil {
			return err
		}
		if (db.Collection != "" || len(db.ExcludeCollections) > 0) && containsAnyOption(db.Args, "--oplog", "--excludeCollectionsWithPrefix", "--query", "-q", "--queryFile") {
			return fmt.Errorf("MongoDB selection for %s cannot be combined with other selection options", db.Name)
		}
		if err := validateDatabaseArgs("MongoDB", db.Args, "--out", "-o", "--archive", "--password", "-p", "--uri", "--config", "--host", "-h", "--port", "--db", "-d", "--username", "-u", "--collection", "-c", "--excludeCollection"); err != nil {
			return err
		}
		if db.ConfigFile != "" {
			path, err := expandPath(db.ConfigFile, base)
			if err != nil {
				return fmt.Errorf("invalid MongoDB config_file: %w", err)
			}
			if err := ensurePrivateFile(path, "MongoDB config"); err != nil {
				return err
			}
			db.ConfigFile = path
		}
	}
	for i := range p.MySQLDatabases {
		db := &p.MySQLDatabases[i]
		if err := checkName("MySQL", db.Name); err != nil {
			return err
		}
		if db.Database == "" {
			return fmt.Errorf("MySQL database is missing: %s", db.Name)
		}
		if db.Port < 0 || db.Port > 65535 {
			return fmt.Errorf("invalid MySQL port for %s", db.Name)
		}
		if db.Host != "" && db.Socket != "" {
			return fmt.Errorf("MySQL database %s must not set both host and socket", db.Name)
		}
		if db.Executable == "" {
			db.Executable = "mysqldump"
		}
		if hasNUL(db.Database, db.Host, db.Socket, db.Username, db.Executable) {
			return fmt.Errorf("MySQL configuration for %s must not contain NUL bytes", db.Name)
		}
		if strings.HasPrefix(db.Database, "-") {
			return fmt.Errorf("MySQL database for %s must not start with a hyphen", db.Name)
		}
		for _, table := range db.Tables {
			if table == "" || strings.HasPrefix(table, "-") || strings.ContainsRune(table, 0) {
				return fmt.Errorf("invalid MySQL table for %s: %q", db.Name, table)
			}
		}
		if err := validateDatabaseArgs("MySQL", db.Args,
			"--defaults-file", "--defaults-extra-file", "--login-path", "--password", "-p",
			"--result-file", "-r", "--host", "-h", "--port", "-P", "--socket", "-S",
			"--user", "-u", "--databases", "--all-databases", "--tables", "--single-transaction",
			"--routines", "-R", "--events", "-E", "--triggers", "--skip-triggers"); err != nil {
			return err
		}
	}
	for i := range p.SQLServerDatabases {
		db := &p.SQLServerDatabases[i]
		if err := checkName("SQL Server", db.Name); err != nil {
			return err
		}
		if db.Database == "" {
			return fmt.Errorf("SQL Server database is missing: %s", db.Name)
		}
		if db.BackupDirectory == "" {
			return fmt.Errorf("SQL Server backup_directory is missing: %s", db.Name)
		}
		if db.Port < 0 || db.Port > 65535 {
			return fmt.Errorf("invalid SQL Server port for %s", db.Name)
		}
		if db.Port != 0 && db.Host == "" {
			return fmt.Errorf("SQL Server port for %s requires a host", db.Name)
		}
		if db.Executable == "" {
			db.Executable = "sqlcmd"
		}
		if hasNUL(db.Database, db.BackupDirectory, db.Host, db.Username, db.Executable) {
			return fmt.Errorf("SQL Server configuration for %s must not contain NUL bytes", db.Name)
		}
		backupDirectory, err := expandPath(db.BackupDirectory, base)
		if err != nil {
			return fmt.Errorf("invalid SQL Server backup_directory for %s: %w", db.Name, err)
		}
		db.BackupDirectory = backupDirectory
		if err := validateDatabaseArgs("SQL Server", db.Args, "-Q", "-q", "-i", "-o", "-P", "-S", "-U", "-d", "-M"); err != nil {
			return err
		}
	}
	return nil
}

func validateNames(kind, database string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid %s for %s: %q", kind, database, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s for %s: %q", kind, database, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func containsAnyOption(args []string, options ...string) bool {
	for _, arg := range args {
		for _, option := range options {
			if arg == option || strings.HasPrefix(arg, option+"=") || (len(option) == 2 && strings.HasPrefix(arg, option) && len(arg) > 2) {
				return true
			}
		}
	}
	return false
}

func hasNUL(values ...string) bool {
	for _, value := range values {
		if strings.ContainsRune(value, 0) {
			return true
		}
	}
	return false
}

func validateDatabaseArgs(backend string, args []string, forbidden ...string) error {
	for _, arg := range args {
		if arg == "" || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("%s args must not contain empty strings or NUL bytes", backend)
		}
		for _, option := range forbidden {
			if arg == option || strings.HasPrefix(arg, option+"=") || (len(option) == 2 && strings.HasPrefix(arg, option) && len(arg) > 2) {
				return fmt.Errorf("%s args must not contain unsafe option %s", backend, option)
			}
		}
	}
	return nil
}
