package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

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
		`"Authorization":"[REDACTED]"`, `"site":"home"`,
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

func TestRedactRepositoryFailsClosedForMalformedURL(t *testing.T) {
	if got := redactRepository("rest:https://user:secret%zz@example.test/repository"); got != redactedValue {
		t.Fatalf("repository = %q", got)
	}
}
