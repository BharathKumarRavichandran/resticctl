package profile

import (
	"strings"
	"time"
)

type SQLiteDatabase struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
}

type DatabaseConnection struct {
	Database string          `json:"database,omitempty"`
	Hosts    []string        `json:"hosts,omitempty"`
	Socket   string          `json:"socket,omitempty"`
	Username string          `json:"username,omitempty"`
	Password *PasswordSource `json:"password,omitempty"`
}

type MongoDBOptions struct {
	ReplicaSet string `json:"replica_set,omitempty"`
	ConfigFile string `json:"config_file,omitempty"`
}

type PostgreSQLOptions struct {
	RequirePrimary bool `json:"require_primary,omitempty"`
}

type PostgreSQLDatabase struct {
	Connection        *DatabaseConnection `json:"connection,omitempty"`
	Options           *PostgreSQLOptions  `json:"options,omitempty"`
	Name              string              `json:"name,omitempty"`
	Database          string              `json:"database,omitempty"`
	Host              string              `json:"host,omitempty"`
	Hosts             []string            `json:"-"`
	Port              int                 `json:"port,omitempty"`
	Username          string              `json:"username,omitempty"`
	Executable        string              `json:"executable,omitempty"`
	Args              []string            `json:"args,omitempty"`
	TablePatterns     []string            `json:"table_patterns,omitempty"`
	Globals           bool                `json:"globals,omitempty"`
	GlobalsExecutable string              `json:"globals_executable,omitempty"`
}

type MongoDBDatabase struct {
	Connection         *DatabaseConnection `json:"connection,omitempty"`
	Options            *MongoDBOptions     `json:"options,omitempty"`
	Name               string              `json:"name,omitempty"`
	Database           string              `json:"database,omitempty"`
	Host               string              `json:"host,omitempty"`
	Hosts              []string            `json:"-"`
	Port               int                 `json:"port,omitempty"`
	Username           string              `json:"username,omitempty"`
	Executable         string              `json:"executable,omitempty"`
	ConfigFile         string              `json:"config_file,omitempty"`
	Args               []string            `json:"args,omitempty"`
	Collection         string              `json:"collection,omitempty"`
	ExcludeCollections []string            `json:"exclude_collections,omitempty"`
}

type MySQLDatabase struct {
	Connection *DatabaseConnection `json:"connection,omitempty"`
	Name       string              `json:"name,omitempty"`
	Database   string              `json:"database,omitempty"`
	Host       string              `json:"host,omitempty"`
	Port       int                 `json:"port,omitempty"`
	Socket     string              `json:"socket,omitempty"`
	Username   string              `json:"username,omitempty"`
	Executable string              `json:"executable,omitempty"`
	Args       []string            `json:"args,omitempty"`
	Tables     []string            `json:"tables,omitempty"`
	Routines   bool                `json:"routines,omitempty"`
	Events     bool                `json:"events,omitempty"`
	Triggers   bool                `json:"triggers,omitempty"`
}

type SQLServerDatabase struct {
	Connection      *DatabaseConnection `json:"connection,omitempty"`
	Name            string              `json:"name,omitempty"`
	Database        string              `json:"database,omitempty"`
	BackupDirectory string              `json:"backup_directory"`
	Host            string              `json:"host,omitempty"`
	Port            int                 `json:"port,omitempty"`
	Username        string              `json:"username,omitempty"`
	Executable      string              `json:"executable,omitempty"`
	Args            []string            `json:"args,omitempty"`
	Compress        bool                `json:"compress,omitempty"`
}

type PasswordSource struct {
	Command []string `json:"command,omitempty"`
	File    string   `json:"file,omitempty"`
	Value   string   `json:"value,omitempty"`
}

func (source PasswordSource) Configured() bool {
	return source.Value != "" || source.File != "" || source.Command != nil
}

type DatabaseCredential struct {
	Password    PasswordSource    `json:"password,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

type Credentials struct {
	Environment          map[string]string             `json:"environment"`
	DatabaseEnvironment  map[string]string             `json:"database_environment,omitempty"`
	DatabaseEnvironments map[string]map[string]string  `json:"database_environments,omitempty"`
	DatabaseCredentials  map[string]DatabaseCredential `json:"databases,omitempty"`
	Password             PasswordSource                `json:"password"`
}

type RepositoryCredentials struct {
	Environment map[string]string `json:"environment,omitempty"`
	Password    PasswordSource    `json:"password"`
}

// DatabaseEnvironmentFor returns shared database values overlaid with values
// scoped to name. The returned map does not alias the credential maps.
func (credentials Credentials) DatabaseEnvironmentFor(name string) map[string]string {
	var specific map[string]string
	for configuredName, environment := range credentials.DatabaseEnvironments {
		if strings.EqualFold(configuredName, name) {
			specific = environment
			break
		}
	}
	if len(credentials.DatabaseEnvironment) == 0 && len(specific) == 0 {
		return nil
	}
	result := make(map[string]string, len(credentials.DatabaseEnvironment)+len(specific))
	for key, value := range credentials.DatabaseEnvironment {
		result[key] = value
	}
	for key, value := range specific {
		setEnvironmentValue(result, key, value)
	}
	return result
}

func setEnvironmentValue(environment map[string]string, key, value string) {
	for existing := range environment {
		if strings.EqualFold(existing, key) {
			delete(environment, existing)
			break
		}
	}
	environment[key] = value
}

func (credentials Credentials) DatabaseCredentialFor(name string) (DatabaseCredential, bool) {
	for configuredName, credential := range credentials.DatabaseCredentials {
		if strings.EqualFold(configuredName, name) {
			return credential, true
		}
	}
	return DatabaseCredential{}, false
}

type Schedule struct {
	Backend string `json:"backend"`
	Cron    string `json:"cron"`
	CatchUp bool   `json:"catch_up"`
}

type ForgetSchedule struct {
	Cron     string `json:"cron,omitempty"`
	Schedule string `json:"schedule,omitempty"` // Deprecated input alias for cron.
	Backend  string `json:"backend"`
	CatchUp  bool   `json:"catch_up"`
	Prune    bool   `json:"prune"`
}

const DefaultHookTimeout = 5 * time.Minute

type Hook struct {
	Command []string `json:"command"`
	Timeout string   `json:"timeout,omitempty"`
}

// Monitoring configures non-fatal observability side effects for recorded actions.
type Monitoring struct {
	HistoryLimit       int              `json:"history_limit,omitempty"`
	StatusFile         string           `json:"status_file,omitempty"`
	PrometheusTextfile string           `json:"prometheus_textfile,omitempty"`
	Pushgateway        *Pushgateway     `json:"pushgateway,omitempty"`
	HTTP               []HTTPHook       `json:"http,omitempty"`
	WarningPolicy      string           `json:"warning_policy,omitempty"`
	BackupStatistics   bool             `json:"backup_statistics,omitempty"`
	Logs               []LogDestination `json:"logs,omitempty"`
}

type Pushgateway struct {
	URL     string            `json:"url"`
	Job     string            `json:"job,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Timeout string            `json:"timeout,omitempty"`
	CAFile  string            `json:"ca_file,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type HTTPHook struct {
	Name         string            `json:"name,omitempty"`
	URL          string            `json:"url"`
	Actions      []string          `json:"actions,omitempty"`
	Phases       []string          `json:"phases,omitempty"`
	Method       string            `json:"method,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
	Timeout      string            `json:"timeout,omitempty"`
	CAFile       string            `json:"ca_file,omitempty"`
}

type LogDestination struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	Address string `json:"address,omitempty"`
	Network string `json:"network,omitempty"`
}

type Profile struct {
	Name                string                   `json:"-"`
	Parent              string                   `json:"parent,omitempty"`
	Repository          string                   `json:"repository"`
	CredentialsFile     string                   `json:"credentials_file,omitempty"`
	PrivateFile         string                   `json:"private_file,omitempty"`
	BackupPaths         []string                 `json:"backup_paths"`
	SQLiteDatabases     []SQLiteDatabase         `json:"sqlite_databases,omitempty"`
	PostgreSQLDatabases []PostgreSQLDatabase     `json:"postgresql_databases,omitempty"`
	MongoDBDatabases    []MongoDBDatabase        `json:"mongodb_databases,omitempty"`
	MySQLDatabases      []MySQLDatabase          `json:"mysql_databases,omitempty"`
	SQLServerDatabases  []SQLServerDatabase      `json:"sqlserver_databases,omitempty"`
	DatabaseConcurrency int                      `json:"database_concurrency,omitempty"`
	ResticArgs          []string                 `json:"restic_args"`
	Commands            map[string]ResticCommand `json:"commands,omitempty"`
	BackupArgs          []string                 `json:"backup_args"`
	Tags                []string                 `json:"tags"`
	ForgetArgs          []string                 `json:"forget_args"`
	CheckArgs           []string                 `json:"check_args"`
	CheckBefore         bool                     `json:"check_before"`
	CheckAfter          bool                     `json:"check_after"`
	PruneBefore         bool                     `json:"prune_before"`
	PruneAfter          bool                     `json:"prune_after"`
	RunBefore           []Hook                   `json:"run_before"`
	RunAfter            []Hook                   `json:"run_after"`
	RunAfterFail        []Hook                   `json:"run_after_fail"`
	RunFinally          []Hook                   `json:"run_finally"`
	Schedule            *Schedule                `json:"schedule,omitempty"`
	Forget              *ForgetSchedule          `json:"forget,omitempty"`
	Monitoring          Monitoring               `json:"monitoring,omitempty"`
	Credentials         Credentials              `json:"-"`
}
