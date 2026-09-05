package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactEndpointUserInfoWithoutPath(t *testing.T) {
	endpoint := "https://user:secret@example.test"
	if got := redactEndpoint(endpoint); got != "https://example.test/<redacted>" {
		t.Fatalf("redacted endpoint = %q", got)
	}
	if !endpointContainsSecrets(endpoint) {
		t.Fatal("endpoint credentials were not detected")
	}
}

func TestRedactedResolvedProfile(t *testing.T) {
	original := Profile{
		Name:       "example",
		Repository: "rest:https://backup:repository-secret@example.test/restic?token=query-secret#fragment-secret",
		Credentials: Credentials{
			Environment: map[string]string{"TOKEN": "credential-secret"},
			Password:    PasswordSource{Command: []string{"secret-command"}},
		},
		Monitoring: Monitoring{
			Pushgateway: &Pushgateway{
				URL:     "https://push.example.test/token-secret?key=query-secret",
				Headers: map[string]string{"Authorization": "header-secret"},
				Labels:  map[string]string{"site": "home"},
			},
			HTTP: []HTTPHook{{
				URL:          "https://hooks.example.test/webhook-secret",
				Headers:      map[string]string{"X-Token": "hook-secret"},
				Body:         "body-secret",
				BodyTemplate: "template-secret",
			}},
		},
	}

	encoded, err := json.Marshal(RedactedResolvedProfile(original))
	if err != nil {
		t.Fatal(err)
	}
	output := string(encoded)
	for _, secret := range []string{
		"repository-secret", "credential-secret", "secret-command", "token-secret",
		"query-secret", "fragment-secret", "header-secret", "webhook-secret", "hook-secret",
		"body-secret", "template-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("resolved profile contains %q: %s", secret, output)
		}
	}
	for _, expected := range []string{
		`"name":"example"`, `backup:REDACTED@example.test`,
		`"Authorization":"\u003credacted\u003e"`, `"site":"home"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("resolved profile does not contain %q: %s", expected, output)
		}
	}

	if original.Repository != "rest:https://backup:repository-secret@example.test/restic?token=query-secret#fragment-secret" ||
		original.Monitoring.Pushgateway.Headers["Authorization"] != "header-secret" ||
		original.Monitoring.HTTP[0].Body != "body-secret" {
		t.Fatal("redaction mutated the loaded profile")
	}
}

func TestRedactedResolvedProfilePreservesNonSecretEndpoints(t *testing.T) {
	original := Profile{Monitoring: Monitoring{HTTP: []HTTPHook{{URL: "https://monitor.example.test"}}}}
	resolved := RedactedResolvedProfile(original)
	if resolved.Monitoring.HTTP[0].URL != "https://monitor.example.test" {
		t.Fatalf("endpoint = %q", resolved.Monitoring.HTTP[0].URL)
	}
}

func TestRedactedResolvedProfileDoesNotMutateConnection(t *testing.T) {
	original := Profile{
		Credentials: Credentials{DatabaseCredentials: map[string]DatabaseCredential{
			"accounts": {Password: PasswordSource{Value: "secret"}},
		}},
		PostgreSQLDatabases: []PostgreSQLDatabase{{
			Name: "accounts", Connection: &DatabaseConnection{Database: "accounts"},
		}},
	}
	resolved := RedactedResolvedProfile(original)
	if original.PostgreSQLDatabases[0].Connection.Password != nil {
		t.Fatal("redaction mutated the original connection")
	}
	if resolved.Databases.PostgreSQL["accounts"].Connection.Password == nil ||
		resolved.Databases.PostgreSQL["accounts"].Connection.Password.Value != redactedValue {
		t.Fatalf("resolved connection password = %#v", resolved.Databases.PostgreSQL["accounts"].Connection.Password)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"name":"accounts"`) {
		t.Fatalf("database name is duplicated inside its map value: %s", encoded)
	}
}

func TestRedactedResolvedProfileHidesMongoDBConfigPath(t *testing.T) {
	original := Profile{MongoDBDatabases: []MongoDBDatabase{{
		Name:       "events",
		Connection: &DatabaseConnection{Database: "events"},
		Options:    &MongoDBOptions{ConfigFile: "/private/mongodb.yaml"},
	}}}
	resolved := RedactedResolvedProfile(original)
	if resolved.Databases.MongoDB["events"].Options.ConfigFile != redactedValue {
		t.Fatalf("resolved config file = %q", resolved.Databases.MongoDB["events"].Options.ConfigFile)
	}
	if original.MongoDBDatabases[0].Options.ConfigFile != "/private/mongodb.yaml" {
		t.Fatal("redaction mutated the original MongoDB options")
	}
}

func TestRedactedResolvedProfileHidesPrivateBindings(t *testing.T) {
	original := Profile{Repository: "local:private-repository", PrivateFile: "/private/config.json", Credentials: Credentials{
		DatabaseCredentials: map[string]DatabaseCredential{"accounts": {Password: PasswordSource{File: "/private/password"}}},
	}, PostgreSQLDatabases: []PostgreSQLDatabase{{
		Name: "accounts", Database: "production_accounts", Hosts: []string{"pg.internal:5432"}, Username: "dbuser-private",
		Connection: &DatabaseConnection{Database: "production_accounts", Hosts: []string{"pg.internal:5432"}, Username: "dbuser-private"},
	}}}
	encoded, err := json.Marshal(RedactedResolvedProfile(original))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"production_accounts", "pg.internal", "dbuser-private"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private value %q appears in %s", private, encoded)
		}
	}
	output := string(encoded)
	for _, expected := range []string{`"repository":"\u003credacted\u003e"`, `"database":"\u003credacted\u003e"`, `"hosts":["\u003credacted\u003e"]`, `"username":"\u003credacted\u003e"`, `"password":{"file":"\u003credacted\u003e"}`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("redacted output does not preserve %q: %s", expected, output)
		}
	}
	if strings.Contains(output, "/private/config.json") || !strings.Contains(output, `"private_file":"\u003credacted\u003e"`) {
		t.Fatalf("private file path was not redacted: %s", output)
	}
	if original.PostgreSQLDatabases[0].Database != "production_accounts" {
		t.Fatal("redaction mutated input")
	}
}

func TestRedactRepositoryFailsClosedForMalformedURL(t *testing.T) {
	if got := redactRepository("rest:https://user:secret%zz@example.test/repository"); got != redactedValue {
		t.Fatalf("repository = %q", got)
	}
}
