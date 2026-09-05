package profile

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"resticctl/internal/securefile"
)

func TestLoadRejectsDuplicateDatabaseNames(t *testing.T) {
	directory := t.TempDir()
	password := filepath.Join(directory, "password")
	writePrivate(t, password, "secret\n")
	credentials := filepath.Join(directory, "credentials.json")
	writePrivate(t, credentials, `{"password":{"file":"password"}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "sqlite_databases":[
            {"name":"Data","path":"one"},
            {"name":"data","path":"two"}
          ]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "duplicate SQLite") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadExternalDatabases(t *testing.T) {
	directory := t.TempDir()
	if err := securefile.Protect(directory); err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"database_environment":{"PGPASSWORD":"private"},"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "mongo.yml"), "password: private\n")
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "databases":{"concurrency":2,
            "postgresql":{"accounts":{"database":"app","host":"db.example","globals":true,"table_patterns":["public.accounts*"]}},
            "mongodb":{"events":{"database":"events","collection":"activity","host":"/var/run/mongodb.sock","config_file":"mongo.yml"}},
            "mysql":{"orders":{"database":"shop","host":"localhost"}},
            "sqlserver":{"warehouse":{"database":"reporting","backup_directory":".","host":"localhost","compress":true}}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PostgreSQLDatabases[0].Executable != "pg_dump" || loaded.PostgreSQLDatabases[0].GlobalsExecutable != "pg_dumpall" {
		t.Fatalf("PostgreSQL defaults = %#v", loaded.PostgreSQLDatabases[0])
	}
	if loaded.MongoDBDatabases[0].ConfigFile != filepath.Join(directory, "mongo.yml") {
		t.Fatalf("MongoDB config = %#v", loaded.MongoDBDatabases[0])
	}
	if !slices.Equal(loaded.PostgreSQLDatabases[0].TablePatterns, []string{"public.accounts*"}) || loaded.MongoDBDatabases[0].Collection != "activity" {
		t.Fatalf("database selectors were not loaded: %#v %#v", loaded.PostgreSQLDatabases[0], loaded.MongoDBDatabases[0])
	}
	if loaded.MySQLDatabases[0].Executable != "mysqldump" {
		t.Fatalf("MySQL defaults = %#v", loaded.MySQLDatabases[0])
	}
	if loaded.SQLServerDatabases[0].Executable != "sqlcmd" || !loaded.SQLServerDatabases[0].Compress {
		t.Fatalf("SQL Server defaults = %#v", loaded.SQLServerDatabases[0])
	}
	if loaded.DatabaseConcurrency != 2 {
		t.Fatalf("database concurrency = %d", loaded.DatabaseConcurrency)
	}
	if loaded.Credentials.DatabaseEnvironment["PGPASSWORD"] != "private" {
		t.Fatal("database environment not loaded")
	}
}

func TestLoadBindsPrivateConnectionOverrides(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "production.private.json"), `{
		  "repository":"local:private",
		  "credentials":{"password":{"value":"restic-secret"}},
		  "databases":{"postgresql":{"accounts":{"connection":{
		    "database":"production_accounts",
		    "hosts":["pg1.internal:5432","pg2.internal:5433"],
		    "username":"backup",
		    "password":{"value":"database-secret"}
		  }}}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:public", "private_file":"production.private.json",
          "databases":{"postgresql":{"accounts":{"connection":{
            "database":"development_accounts", "hosts":["localhost:5432"], "username":"developer"
          }}}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	database := loaded.PostgreSQLDatabases[0]
	if loaded.Repository != "local:private" || database.Database != "production_accounts" || database.Username != "backup" || !slices.Equal(database.Hosts, []string{"pg1.internal:5432", "pg2.internal:5433"}) {
		t.Fatalf("private overrides were not bound: repository=%q database=%#v", loaded.Repository, database)
	}
	credential, ok := loaded.Credentials.DatabaseCredentialFor("accounts")
	if !ok || credential.Password.Value != "database-secret" {
		t.Fatalf("database credential = %#v, %v", credential, ok)
	}
	if database.Connection.Password != nil {
		t.Fatal("password remained in displayable connection")
	}
}

func TestLoadBindsPrivateMongoDBOptions(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "mongo.yml"), "password: private\n")
	writePrivate(t, filepath.Join(directory, "production.private.json"), `{
		  "repository":"local:private",
		  "credentials":{"password":{"value":"restic-secret"}},
		  "databases":{"mongodb":{"events":{
		    "connection":{"hosts":["mongo1.internal:27017","mongo2.internal:27017"],"username":"backup"},
		    "options":{"replica_set":"events-rs","config_file":"mongo.yml"}
		  }}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:public", "private_file":"production.private.json",
          "databases":{"mongodb":{"events":{"connection":{"database":"events"}}}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	database := loaded.MongoDBDatabases[0]
	if database.Options == nil || database.Options.ReplicaSet != "events-rs" || database.ConfigFile != filepath.Join(directory, "mongo.yml") {
		t.Fatalf("MongoDB options = %#v, config file = %q", database.Options, database.ConfigFile)
	}
}

func TestLoadAllowsPublicProfilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"value":"secret"}}`)
	profilePath := filepath.Join(directory, "example.json")
	if err := os.WriteFile(profilePath, []byte(`{"repository":"local:test","credentials_file":"credentials.json"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(profilePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory, "example"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAcceptsInlineRepositoryCredentials(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials":{"environment":{"TOKEN":"private"},"password":{"value":"secret"}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Credentials.Environment["TOKEN"] != "private" || loaded.Credentials.Password.Value != "secret" {
		t.Fatalf("credentials = %#v", loaded.Credentials)
	}
}

func TestLoadResolvesPublicPasswordFileBeforePrivateMerge(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "repository-password"), "secret\n")
	privateDirectory := filepath.Join(directory, "private")
	if err := os.Mkdir(privateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(privateDirectory, "example.private.json"), `{
          "credentials":{"environment":{"TOKEN":"private"}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "private_file":"private/example.private.json",
          "credentials":{"password":{"file":"repository-password"}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Credentials.Password.File != filepath.Join(directory, "repository-password") {
		t.Fatalf("password file = %q", loaded.Credentials.Password.File)
	}
}

func TestLoadRequiresPrivatePermissionsForInlineCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "example.json")
	if err := os.WriteFile(path, []byte(`{
          "repository":"local:test",
          "credentials":{"password":{"value":"secret"}}
        }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "profile containing secrets") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRequiresPrivatePermissionsForSensitivePublicFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	for name, fields := range map[string]string{
		"repository URL": `"repository":"rest:https://user:secret@example.test/repository"`,
		"monitoring":     `"repository":"local:test","monitoring":{"http":[{"url":"https://example.test/token","headers":{"Authorization":"secret"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"value":"repository-secret"}}`)
			path := filepath.Join(directory, "example.json")
			content := `{"credentials_file":"credentials.json",` + fields + `}`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "profile containing secrets") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadRejectsPrivateDatabaseUnderWrongProvider(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "production.private.json"), `{
          "credentials":{"password":{"value":"secret"}},
          "databases":{"mysql":{"accounts":{"connection":{"database":"wrong"}}}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "private_file":"production.private.json",
          "databases":{"postgresql":{"accounts":{"connection":{"database":"accounts"}}}}
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unknown mysql database") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsProviderOptionsInWrongPrivateEntry(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.private.json"), `{
          "credentials":{"password":{"value":"secret"}},
          "databases":{"postgresql":{"accounts":{"options":{"replica_set":"rs0"}}}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "private_file":"example.private.json",
          "databases":{"postgresql":{"accounts":{"connection":{"database":"accounts"}}}}
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsEmptyConnectionPasswordSource(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials":{"password":{"value":"repository-secret"}},
          "databases":{"postgresql":{"accounts":{"connection":{
            "database":"accounts","password":{}
          }}}}
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUnsafeSQLServerConfiguration(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	for name, database := range map[string]string{
		"missing database":         `{"name":"warehouse"}`,
		"missing backup directory": `{"name":"warehouse","database":"reporting"}`,
		"port without host":        `{"name":"warehouse","database":"reporting","backup_directory":".","port":1433}`,
		"password argument":        `{"name":"warehouse","database":"reporting","backup_directory":".","args":["-Psecret"]}`,
		"query override":           `{"name":"warehouse","database":"reporting","backup_directory":".","args":["-Q","SELECT 1"]}`,
		"output redirection":       `{"name":"warehouse","database":"reporting","backup_directory":".","args":["-o=result.txt"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json","sqlserver_databases":[`+database+`]}`)
			if _, err := Load(directory, "example"); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestLoadRejectsMixedNestedAndLegacyDatabaseConfiguration(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "databases":{"sqlite":{}},
          "sqlite_databases":[]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not be combined") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUnsafeMySQLConfiguration(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	for name, database := range map[string]string{
		"credential argument":   `{"name":"orders","database":"shop","args":["--defaults-extra-file=/tmp/exposed"]}`,
		"conflicting endpoints": `{"name":"orders","database":"shop","host":"localhost","socket":"/run/mysql.sock"}`,
		"unsafe table":          `{"name":"orders","database":"shop","tables":["--all-databases"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json","mysql_databases":[`+database+`]}`)
			if _, err := Load(directory, "example"); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestLoadRejectsDatabaseCredentialArguments(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "mongodb_databases":[{"name":"events","args":["--password=exposed"]}]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unsafe option --password") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsInvalidDatabaseSelectors(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	tests := map[string]string{
		"empty PostgreSQL table":               `"postgresql_databases":[{"name":"app","database":"app","table_patterns":[""]}]`,
		"PostgreSQL table argument":            `"postgresql_databases":[{"name":"app","database":"app","table_patterns":["users"],"args":["--exclude-table=audit"]}]`,
		"collection without database":          `"mongodb_databases":[{"name":"events","collection":"activity"}]`,
		"exclusions without database":          `"mongodb_databases":[{"name":"events","exclude_collections":["cache"]}]`,
		"collection and exclusions":            `"mongodb_databases":[{"name":"events","database":"events","collection":"activity","exclude_collections":["cache"]}]`,
		"empty excluded collection":            `"mongodb_databases":[{"name":"events","database":"events","exclude_collections":[""]}]`,
		"duplicate excluded collection":        `"mongodb_databases":[{"name":"events","database":"events","exclude_collections":["cache","cache"]}]`,
		"MongoDB collection selector conflict": `"mongodb_databases":[{"name":"events","database":"events","args":["--collection=activity"]}]`,
		"MongoDB exclusion argument":           `"mongodb_databases":[{"name":"events","database":"events","args":["--excludeCollection=cache"]}]`,
		"MongoDB collection with oplog":        `"mongodb_databases":[{"name":"events","database":"events","collection":"activity","args":["--oplog=true"]}]`,
		"MongoDB exclusions with oplog":        `"mongodb_databases":[{"name":"events","database":"events","exclude_collections":["cache"],"args":["--oplog"]}]`,
	}
	for name, configured := range tests {
		t.Run(name, func(t *testing.T) {
			writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json",`+configured+`}`)
			if _, err := Load(directory, "example"); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestDatabaseEnvironmentForUsesOnlySharedAndNamedValues(t *testing.T) {
	credentials := Credentials{
		DatabaseEnvironment: map[string]string{"SHARED": "value", "OVERRIDE": "shared"},
		DatabaseEnvironments: map[string]map[string]string{
			"accounts": {"PGPASSWORD": "private", "OVERRIDE": "specific"},
			"events":   {"MONGO_TOKEN": "private"},
		},
	}
	environment := credentials.DatabaseEnvironmentFor("ACCOUNTS")
	if environment["SHARED"] != "value" || environment["PGPASSWORD"] != "private" || environment["OVERRIDE"] != "specific" {
		t.Fatalf("environment = %v", environment)
	}
	if _, exists := environment["MONGO_TOKEN"]; exists {
		t.Fatal("environment leaked credentials from another database")
	}
}

func TestDatabaseEnvironmentForOverlaysKeysCaseInsensitively(t *testing.T) {
	credentials := Credentials{
		DatabaseEnvironment:  map[string]string{"TOKEN": "shared"},
		DatabaseEnvironments: map[string]map[string]string{"accounts": {"token": "specific"}},
	}
	environment := credentials.DatabaseEnvironmentFor("accounts")
	if len(environment) != 1 || environment["token"] != "specific" {
		t.Fatalf("database environment = %#v", environment)
	}
}

func TestLoadPreservesLegacyEnvironmentWithConnectionPassword(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
		"database_environments":{"accounts":{"CUSTOM":"value"}},
		"password":{"value":"repository-secret"}
	}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
		"repository":"local:test", "credentials_file":"credentials.json",
		"databases":{"postgresql":{"accounts":{"connection":{
			"database":"accounts", "password":{"value":"database-secret"}
		}}}}
	}`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if environment := loaded.Credentials.DatabaseEnvironmentFor("accounts"); environment["CUSTOM"] != "value" {
		t.Fatalf("database environment = %#v", environment)
	}
	credential, ok := loaded.Credentials.DatabaseCredentialFor("accounts")
	if !ok || credential.Password.Value != "database-secret" {
		t.Fatalf("database credential = %#v, %v", credential, ok)
	}
}

func TestLoadKeepsTypedDatabasePasswordsSeparateFromEnvironment(t *testing.T) {
	directory := t.TempDir()
	if err := securefile.Protect(directory); err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
		  "databases":{"warehouse":{"password":{"value":"private"},"environment":{"CUSTOM":"value"}}},
          "password":{"command":["password-command"]}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "databases":{"sqlserver":{"warehouse":{"database":"reporting","backup_directory":"."}}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	environment := loaded.Credentials.DatabaseEnvironmentFor("warehouse")
	if _, exists := environment["SQLSERVER_PASSWORD"]; exists || environment["CUSTOM"] != "value" {
		t.Fatalf("database environment = %#v", environment)
	}
	credential, ok := loaded.Credentials.DatabaseCredentialFor("warehouse")
	if !ok || credential.Password.Value != "private" {
		t.Fatalf("database credential = %#v, %v", credential, ok)
	}
}

func TestLoadRejectsMixedDatabaseCredentialFormats(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
          "database_environment":{}, "databases":{},
          "password":{"command":["password-command"]}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json"}`)
	if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "must not be combined") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUnknownNamedDatabaseEnvironment(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
          "database_environments":{"missing":{"PASSWORD":"private"}},
          "password":{"command":["password-command"]}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "postgresql_databases":[{"name":"accounts","database":"app"}]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unknown database") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadMonitoringDefaultsAndResolvesPaths(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "monitoring":{
            "status_file":"exports/status.json", "prometheus_textfile":"exports/status.prom",
            "http":[{"url":"https://monitor.example/events"}],
            "logs":[{"type":"file","path":"events.jsonl"}]
          }
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Monitoring.HistoryLimit != 100 || loaded.Monitoring.WarningPolicy != "failure" {
		t.Fatalf("monitoring defaults = %#v", loaded.Monitoring)
	}
	if loaded.Monitoring.StatusFile != filepath.Join(directory, "exports/status.json") || loaded.Monitoring.Logs[0].Path != filepath.Join(directory, "events.jsonl") {
		t.Fatalf("monitoring paths = %#v", loaded.Monitoring)
	}
}

func TestLoadRejectsUnsafeMonitoringConfiguration(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	for name, monitoring := range map[string]string{
		"credential URL":       `{"http":[{"url":"https://user:secret@monitor.example/events"}]}`,
		"credential overwrite": `{"status_file":"credentials.json"}`,
		"header newline":       `{"http":[{"url":"https://monitor.example/events","headers":{"X-Test":"bad\nvalue"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json","monitoring":`+monitoring+`}`)
			if _, err := Load(directory, "example"); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestLoadResolvesNestedInheritance(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "base.json"), `{
          "repository":"local:base",
          "backup_paths":["base-files"],
          "restic_args":["--retry-lock","1m"],
          "tags":["base"],
          "check_before":true,
          "run_before":[{"command":["base-hook"]}],
          "sqlite_databases":[{"name":"main","path":"base.sqlite"}]
        }`)
	writePrivate(t, filepath.Join(directory, "middle.json"), `{
          "parent":"base",
          "backup_args":["--exclude-caches"],
          "tags":["middle"],
          "sqlite_databases":[{"name":"extra","path":"extra.sqlite"}]
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "parent":"middle",
          "repository":"local:child",
          "credentials_file":"credentials.json",
          "backup_paths":["child-files"],
          "tags":["child"],
          "check_before":false,
          "run_before":[{"command":["child-hook"]}],
          "sqlite_databases":[{"name":"MAIN","path":"child.sqlite"}]
        }`)

	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repository != "local:child" || loaded.CheckBefore {
		t.Fatalf("scalar overrides not applied: %#v", loaded)
	}
	if strings.Join(loaded.Tags, ",") != "child" || len(loaded.BackupPaths) != 1 || len(loaded.RunBefore) != 1 {
		t.Fatalf("child arrays did not replace inherited arrays: %#v", loaded)
	}
	if len(loaded.SQLiteDatabases) != 1 || loaded.SQLiteDatabases[0].Name != "MAIN" || !strings.HasSuffix(loaded.SQLiteDatabases[0].Path, "child.sqlite") {
		t.Fatalf("SQLite merge = %#v", loaded.SQLiteDatabases)
	}
	if loaded.CredentialsFile != filepath.Join(directory, "credentials.json") {
		t.Fatalf("credentials file = %q", loaded.CredentialsFile)
	}
}

func TestLoadMergesAndValidatesPersistentResticCommands(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "base.json"), `{
          "repository":"local:test",
          "commands":{"mount":{"args":["--allow-other"]}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "parent":"base",
          "credentials_file":"credentials.json",
          "commands":{"mount":{"args":["--no-default-permissions"]},"rewrite":{"args":["--dry-run"]}}
        }`)

	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Commands["mount"].Args; !slices.Equal(got, []string{"--no-default-permissions"}) {
		t.Fatalf("mount args = %v", got)
	}

	writePrivate(t, filepath.Join(directory, "invalid.json"), `{
          "repository":"local:test","credentials_file":"credentials.json",
          "commands":{"mont":{"args":[]}}
        }`)
	if _, err := Load(directory, "invalid"); err == nil || !strings.Contains(err.Error(), "unsupported Restic command") {
		t.Fatalf("Load error = %v", err)
	}

	writePrivate(t, filepath.Join(directory, "reserved.json"), `{
          "repository":"local:test","credentials_file":"credentials.json",
          "commands":{"mount":{"args":["--password-file=secret"]}}
        }`)
	if _, err := Load(directory, "reserved"); err == nil || !strings.Contains(err.Error(), "must not override") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsInheritanceCycle(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "one.json"), `{"parent":"two"}`)
	writePrivate(t, filepath.Join(directory, "two.json"), `{"parent":"one"}`)
	_, err := Load(directory, "one")
	if err == nil || !strings.Contains(err.Error(), "one -> two -> one") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsMissingAndInvalidParent(t *testing.T) {
	for name, parent := range map[string]string{"missing": "absent", "invalid": "../escape"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "example.json"), `{"parent":"`+parent+`"}`)
			_, err := Load(directory, "example")
			if err == nil || !strings.Contains(err.Error(), "parent") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadDoesNotInheritCredentialsAndValidatesResolvedProfile(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "base-credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "base.json"), `{
          "repository":"local:test",
          "credentials_file":"base-credentials.json",
          "backup_args":["--repo=forbidden"]
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{"parent":"base"}`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "credentials_file") {
		t.Fatalf("Load error = %v", err)
	}

	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "valid-child.json"), `{"parent":"base","credentials_file":"credentials.json"}`)
	_, err = Load(directory, "valid-child")
	if err == nil || !strings.Contains(err.Error(), "must not override") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadDoesNotInheritCaseVariedCredentialFields(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "base.json"), `{
		"repository":"local:test",
		"Credentials":{"password":{"value":"parent-secret"}},
		"Private_File":"parent.private.json"
	}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{"parent":"base"}`)

	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "set private_file, credentials_file, or valid inline credentials") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadArraysReplaceInheritedValues(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "base.json"), `{
          "repository":"local:test",
          "backup_paths":["base-files"],
          "tags":["base"],
          "run_before":[{"command":["base-hook"]}],
          "sqlite_databases":[{"name":"base","path":"base.sqlite"}],
          "schedule":{"cron":"0 2 * * *"}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "parent":"base",
          "credentials_file":"credentials.json",
		  "backup_paths":["child-files"],
		  "tags":[],
		  "run_before":[],
		  "schedule":null
        }`)

	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.BackupPaths) != 1 || !strings.HasSuffix(loaded.BackupPaths[0], "child-files") {
		t.Fatalf("backup paths = %v", loaded.BackupPaths)
	}
	if len(loaded.Tags) != 0 || len(loaded.RunBefore) != 0 || loaded.Schedule != nil {
		t.Fatalf("inherited values were not cleared: %#v", loaded)
	}
}

func TestLoadMergesInheritedDatabaseMapsByName(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "base.json"), `{
          "repository":"local:test",
          "databases":{"postgresql":{"app":{"database":"app"}}}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "parent":"base",
          "credentials_file":"credentials.json",
		  "databases":{"postgresql":{"audit":{"database":"audit"}}}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PostgreSQLDatabases) != 2 || loaded.PostgreSQLDatabases[0].Name != "app" || loaded.PostgreSQLDatabases[1].Name != "audit" {
		t.Fatalf("inherited PostgreSQL databases = %#v", loaded.PostgreSQLDatabases)
	}
}

func TestLoadRecursivelyMergesInheritedDatabaseEntry(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "base.json"), `{
		"repository":"local:test",
		"databases":{"postgresql":{"Accounts":{
			"connection":{"database":"accounts","hosts":["pg1:5432"],"username":"backup","Password":{"command":["old-password"]}},
			"options":{"require_primary":false},
			"table_patterns":["public.customers"]
		}}}
	}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
		"parent":"base",
		"credentials_file":"credentials.json",
		"databases":{"postgresql":{"accounts":{
			"connection":{"hosts":["pg2:5432","pg3:5432"],"password":{"value":"new-password"}},
			"options":{"require_primary":true},
			"table_patterns":[]
		}}}
	}`)

	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	database := loaded.PostgreSQLDatabases[0]
	if database.Name != "accounts" || database.Database != "accounts" || database.Username != "backup" ||
		!slices.Equal(database.Hosts, []string{"pg2:5432", "pg3:5432"}) ||
		database.Options == nil || !database.Options.RequirePrimary || database.TablePatterns == nil || len(database.TablePatterns) != 0 {
		t.Fatalf("merged database = %#v", database)
	}
	credential, ok := loaded.Credentials.DatabaseCredentialFor("accounts")
	if !ok || credential.Password.Value != "new-password" || credential.Password.Command != nil {
		t.Fatalf("merged database password = %#v, %v", credential.Password, ok)
	}
}

func TestLoadMergesCaseVariedFieldsAndDatabaseNamedPassword(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "base.json"), `{
		"Repository":"local:base",
		"Databases":{"PostgreSQL":{"password":{
			"Connection":{"Database":"accounts","Username":"backup"},
			"globals":true
		}}}
	}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
		"parent":"base", "repository":"local:child",
		"credentials":{"password":{"value":"secret"}},
		"databases":{"postgresql":{"password":{
			"connection":{"database":"child_accounts"}
		}}}
	}`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repository != "local:child" || len(loaded.PostgreSQLDatabases) != 1 {
		t.Fatalf("overrides were not merged: %#v", loaded)
	}
	db := loaded.PostgreSQLDatabases[0]
	if db.Database != "child_accounts" || db.Username != "backup" || !db.Globals {
		t.Fatalf("database settings were lost: %#v", db)
	}
}

func TestConnectionRejectsPortWithoutHostname(t *testing.T) {
	for _, host := range []string{":5432", "[]:5432"} {
		if err := validateConnection("PostgreSQL", "accounts", &DatabaseConnection{Hosts: []string{host}}, true); err == nil {
			t.Fatalf("accepted host %q", host)
		}
	}
}

func TestLoadRejectsArrayDatabaseCollectionsAndEmbeddedNames(t *testing.T) {
	for name, databases := range map[string]string{
		"array":         `{"postgresql":[]}`,
		"embedded name": `{"postgresql":{"accounts":{"name":"other"}}}`,
		"invalid key":   `{"postgresql":{"../accounts":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "example.json"), `{"databases":`+databases+`}`)
			if _, err := Load(directory, "example"); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}

func TestLoadRejectsRemovedReplaceInheritedField(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{"replace_inherited":["tags","tags","unknown"]}`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "replace_inherited") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsNonPositiveDatabaseConcurrency(t *testing.T) {
	for _, concurrency := range []string{"0", "-1"} {
		t.Run(concurrency, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
			writePrivate(t, filepath.Join(directory, "example.json"), `{
              "repository":"local:test", "credentials_file":"credentials.json",
              "database_concurrency":`+concurrency+`
            }`)
			_, err := Load(directory, "example")
			if err == nil || !strings.Contains(err.Error(), "database_concurrency") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadRejectsUnknownJSONField(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "typo":true
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsDuplicateJSONField(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:one",
          "Repository":"local:two",
          "credentials_file":"credentials.json"
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "duplicate JSON field") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsEmptyBackupPath(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":[""]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "backup_paths") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestValidateNameRejectsNonPortableNames(t *testing.T) {
	for _, name := range []string{"", "../escape", "trailing.", "CON", "con.txt", "LPT9", strings.Repeat("a", maxNameLength+1)} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) succeeded", name)
		}
	}
	for _, name := range []string{"home", "home-server", "photos.2026", "auxiliary"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q): %v", name, err)
		}
	}
}

func TestExpandPathRejectsUnsetEnvironmentVariable(t *testing.T) {
	const name = "RESTICCTL_TEST_MISSING_VARIABLE"
	t.Setenv(name, "temporary")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	_, err := expandPath("${"+name+"}/files", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), name) {
		t.Fatalf("expandPath error = %v", err)
	}
}

func TestLoadRejectsReservedResticOptions(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":["files"],
          "backup_args":["--repo=other"]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not override") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsConfiguredBackupDryRunOptions(t *testing.T) {
	for _, test := range []struct {
		name, field string
	}{
		{"long", `"backup_args":["--dry-run"]`},
		{"short", `"backup_args":["-n"]`},
		{"explicit true", `"backup_args":["--dry-run=true"]`},
		{"command arguments", `"commands":{"backup":{"args":["-n=true"]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
			writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json",`+test.field+`}`)
			_, err := Load(directory, "example")
			if err == nil || !strings.Contains(err.Error(), "dry-run") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadRejectsPostgreSQLDatabaseBeginningWithHyphen(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json","postgresql_databases":[{"name":"main","database":"--help"}]}`)
	if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "must not start with a hyphen") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsReservedResticEnvironment(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
          "environment":{"RESTIC_PASSWORD":"secret"},
          "password":{"command":["password-command"]}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json"
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not set RESTIC_PASSWORD") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRequiresForgetArgsForBackupPrune(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":["files"],
          "prune_after":true
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "requires non-empty forget_args") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadHooks(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "run_before":[{"command":["prepare","--quiet"],"timeout":"30s"}],
          "run_finally":[{"command":["cleanup"]}]
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.RunBefore) != 1 || loaded.RunBefore[0].Timeout != "30s" || len(loaded.RunFinally) != 1 {
		t.Fatalf("hooks = %#v, %#v", loaded.RunBefore, loaded.RunFinally)
	}
}

func TestLoadRejectsInvalidHooks(t *testing.T) {
	for _, hook := range []string{
		`{"command":[]}`,
		`{"command":["valid",""]}`,
		`{"command":["valid"],"timeout":"never"}`,
		`{"command":["valid"],"timeout":"0s"}`,
	} {
		t.Run(hook, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
			writePrivate(t, filepath.Join(directory, "example.json"), `{
              "repository":"local:test",
              "credentials_file":"credentials.json",
              "run_before":[`+hook+`]
            }`)
			if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "run_before") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadSchedule(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "forget_args":["--keep-daily","7"],
          "schedule":{"cron":" 0  2 * * * ","catch_up":true},
		  "forget":{"cron":"@daily","catch_up":true,"prune":true}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Schedule == nil || loaded.Schedule.Backend != "auto" || loaded.Schedule.Cron != "0 2 * * *" || !loaded.Schedule.CatchUp {
		t.Fatalf("schedule = %#v", loaded.Schedule)
	}
	if loaded.Forget == nil || loaded.Forget.Backend != "auto" || loaded.Forget.Cron != "0 0 * * *" || !loaded.Forget.CatchUp || !loaded.Forget.Prune {
		t.Fatalf("forget = %#v", loaded.Forget)
	}
}

func TestLoadAcceptsDeprecatedForgetScheduleAlias(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "forget_args":["--keep-last","1"], "forget":{"schedule":"weekly"}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Forget.Cron != "0 0 * * 0" || loaded.Forget.Schedule != "" {
		t.Fatalf("forget = %#v", loaded.Forget)
	}
}

func TestLoadRejectsBothForgetScheduleFields(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test", "credentials_file":"credentials.json",
          "forget_args":["--keep-last","1"], "forget":{"cron":"daily","schedule":"weekly"}
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "both cron") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsInvalidSchedule(t *testing.T) {
	for _, configuredSchedule := range []string{
		`{"backend":"bogus","cron":"0 2 * * *"}`,
		`{"backend":"auto","cron":"99 2 * * *"}`,
	} {
		t.Run(configuredSchedule, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
			writePrivate(t, filepath.Join(directory, "example.json"), `{
              "repository":"local:test",
              "credentials_file":"credentials.json",
              "schedule":`+configuredSchedule+`
            }`)
			if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "schedule") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadRequiresPrivateCredentialsFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	directory := t.TempDir()
	credentials := filepath.Join(directory, "credentials.json")
	writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json"}`)
	if err := os.WriteFile(credentials, []byte(`{"password":{"command":["printf","secret"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credentials, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("Load error = %v", err)
	}
}

func writePrivate(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securefile.Protect(path); err != nil {
		t.Fatal(err)
	}
}
