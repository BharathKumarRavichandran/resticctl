package profile

import (
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
	backupProfile.Name = name
	if backupProfile.Repository == "" {
		return Profile{}, errors.New("repository must be a non-empty string")
	}
	if strings.ContainsRune(backupProfile.Repository, 0) {
		return Profile{}, errors.New("repository must not contain NUL bytes")
	}
	if backupProfile.CredentialsFile == "" {
		return Profile{}, errors.New("credentials_file must be a non-empty string")
	}

	base := filepath.Dir(profilePath)
	credentialsPath, err := expandPath(backupProfile.CredentialsFile, base)
	if err != nil {
		return Profile{}, fmt.Errorf("invalid credentials_file: %w", err)
	}
	backupProfile.CredentialsFile = credentialsPath
	credentials, err := loadCredentials(credentialsPath)
	if err != nil {
		return Profile{}, err
	}
	backupProfile.Credentials = credentials

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
	if err := validateDatabaseEnvironmentNames(backupProfile); err != nil {
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

// profileConfig uses pointers for scalars so inheritance can distinguish an
// omitted value from an explicit false or empty value.
type profileConfig struct {
	Parent              string                   `json:"parent,omitempty"`
	ReplaceInherited    []string                 `json:"replace_inherited,omitempty"`
	Repository          *string                  `json:"repository"`
	CredentialsFile     *string                  `json:"credentials_file"`
	BackupPaths         []string                 `json:"backup_paths"`
	SQLiteDatabases     []SQLiteDatabase         `json:"sqlite_databases"`
	PostgreSQLDatabases []PostgreSQLDatabase     `json:"postgresql_databases,omitempty"`
	MongoDBDatabases    []MongoDBDatabase        `json:"mongodb_databases,omitempty"`
	MySQLDatabases      []MySQLDatabase          `json:"mysql_databases,omitempty"`
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
	Concurrency *int                 `json:"concurrency,omitempty"`
	SQLite      []SQLiteDatabase     `json:"sqlite,omitempty"`
	PostgreSQL  []PostgreSQLDatabase `json:"postgresql,omitempty"`
	MongoDB     []MongoDBDatabase    `json:"mongodb,omitempty"`
	MySQL       []MySQLDatabase      `json:"mysql,omitempty"`
}

func resolve(configDir, name string, chain []string) (profileConfig, error) {
	if err := ValidateName(name); err != nil {
		return profileConfig{}, fmt.Errorf("invalid parent profile %q: %w", name, err)
	}
	for _, ancestor := range chain {
		if strings.EqualFold(ancestor, name) {
			return profileConfig{}, fmt.Errorf("profile inheritance cycle: %s", strings.Join(append(chain, name), " -> "))
		}
	}
	path := filepath.Join(configDir, name+".json")
	if err := ensurePrivateFile(path, "profile"); err != nil {
		if len(chain) > 0 {
			return profileConfig{}, fmt.Errorf("cannot load parent profile %q: %w", name, err)
		}
		return profileConfig{}, err
	}
	var child profileConfig
	if err := decodeStrict(path, "profile", &child); err != nil {
		return profileConfig{}, err
	}
	if err := child.normalizeDatabases(); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	if err := validateReplaceInherited(child.ReplaceInherited); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	if err := validateConfiguredDatabases(child.SQLiteDatabases); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	if err := validateConfiguredNames(child.PostgreSQLDatabases, func(v PostgreSQLDatabase) string { return v.Name }); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s PostgreSQL databases: %w", name, err)
	}
	if err := validateConfiguredNames(child.MongoDBDatabases, func(v MongoDBDatabase) string { return v.Name }); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s MongoDB databases: %w", name, err)
	}
	if err := validateConfiguredNames(child.MySQLDatabases, func(v MySQLDatabase) string { return v.Name }); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s MySQL databases: %w", name, err)
	}
	if child.Parent == "" {
		return child, nil
	}
	parent, err := resolve(configDir, child.Parent, append(chain, name))
	if err != nil {
		return profileConfig{}, err
	}
	return merge(parent, child), nil
}

func (configured *profileConfig) normalizeDatabases() error {
	for index, field := range configured.ReplaceInherited {
		switch field {
		case "databases.sqlite":
			configured.ReplaceInherited[index] = "sqlite_databases"
		case "databases.postgresql":
			configured.ReplaceInherited[index] = "postgresql_databases"
		case "databases.mongodb":
			configured.ReplaceInherited[index] = "mongodb_databases"
		case "databases.mysql":
			configured.ReplaceInherited[index] = "mysql_databases"
		}
	}
	if configured.Databases == nil {
		return nil
	}
	if configured.SQLiteDatabases != nil || configured.PostgreSQLDatabases != nil || configured.MongoDBDatabases != nil ||
		configured.MySQLDatabases != nil || configured.DatabaseConcurrency != nil {
		return errors.New("databases must not be combined with legacy top-level database fields")
	}
	configured.DatabaseConcurrency = configured.Databases.Concurrency
	configured.SQLiteDatabases = configured.Databases.SQLite
	configured.PostgreSQLDatabases = configured.Databases.PostgreSQL
	configured.MongoDBDatabases = configured.Databases.MongoDB
	configured.MySQLDatabases = configured.Databases.MySQL
	configured.Databases = nil
	return nil
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
		if hasNUL(db.Database, db.Host, db.Executable, db.ConfigFile) {
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
		if err := validateDatabaseArgs("MongoDB", db.Args, "--out", "-o", "--archive", "--password", "-p", "--uri", "--config", "--host", "-h", "--port", "--db", "-d", "--collection", "-c", "--excludeCollection"); err != nil {
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
