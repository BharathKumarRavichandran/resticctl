package profile

import (
	"strings"
	"time"
)

type SQLiteDatabase struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type PostgreSQLDatabase struct {
	Name              string   `json:"name"`
	Database          string   `json:"database"`
	Host              string   `json:"host,omitempty"`
	Port              int      `json:"port,omitempty"`
	Username          string   `json:"username,omitempty"`
	Executable        string   `json:"executable,omitempty"`
	Args              []string `json:"args,omitempty"`
	Globals           bool     `json:"globals,omitempty"`
	GlobalsExecutable string   `json:"globals_executable,omitempty"`
}

type MongoDBDatabase struct {
	Name       string   `json:"name"`
	Database   string   `json:"database,omitempty"`
	Host       string   `json:"host,omitempty"`
	Port       int      `json:"port,omitempty"`
	Executable string   `json:"executable,omitempty"`
	ConfigFile string   `json:"config_file,omitempty"`
	Args       []string `json:"args,omitempty"`
}

type MySQLDatabase struct {
	Name       string   `json:"name"`
	Database   string   `json:"database"`
	Host       string   `json:"host,omitempty"`
	Port       int      `json:"port,omitempty"`
	Socket     string   `json:"socket,omitempty"`
	Username   string   `json:"username,omitempty"`
	Executable string   `json:"executable,omitempty"`
	Args       []string `json:"args,omitempty"`
	Tables     []string `json:"tables,omitempty"`
	Routines   bool     `json:"routines,omitempty"`
	Events     bool     `json:"events,omitempty"`
	Triggers   bool     `json:"triggers,omitempty"`
}

type PasswordSource struct {
	Command []string `json:"command"`
	File    string   `json:"file"`
}

type Credentials struct {
	Environment          map[string]string            `json:"environment"`
	DatabaseEnvironment  map[string]string            `json:"database_environment,omitempty"`
	DatabaseEnvironments map[string]map[string]string `json:"database_environments,omitempty"`
	Password             PasswordSource               `json:"password"`
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
		result[key] = value
	}
	return result
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

type Profile struct {
	Name                string               `json:"-"`
	Parent              string               `json:"parent,omitempty"`
	Repository          string               `json:"repository"`
	CredentialsFile     string               `json:"credentials_file"`
	BackupPaths         []string             `json:"backup_paths"`
	SQLiteDatabases     []SQLiteDatabase     `json:"sqlite_databases"`
	PostgreSQLDatabases []PostgreSQLDatabase `json:"postgresql_databases,omitempty"`
	MongoDBDatabases    []MongoDBDatabase    `json:"mongodb_databases,omitempty"`
	MySQLDatabases      []MySQLDatabase      `json:"mysql_databases,omitempty"`
	DatabaseConcurrency int                  `json:"database_concurrency,omitempty"`
	ResticArgs          []string             `json:"restic_args"`
	BackupArgs          []string             `json:"backup_args"`
	Tags                []string             `json:"tags"`
	ForgetArgs          []string             `json:"forget_args"`
	CheckArgs           []string             `json:"check_args"`
	CheckBefore         bool                 `json:"check_before"`
	CheckAfter          bool                 `json:"check_after"`
	PruneBefore         bool                 `json:"prune_before"`
	PruneAfter          bool                 `json:"prune_after"`
	RunBefore           []Hook               `json:"run_before"`
	RunAfter            []Hook               `json:"run_after"`
	RunAfterFail        []Hook               `json:"run_after_fail"`
	RunFinally          []Hook               `json:"run_finally"`
	Schedule            *Schedule            `json:"schedule,omitempty"`
	Forget              *ForgetSchedule      `json:"forget,omitempty"`
	Credentials         Credentials          `json:"-"`
}
