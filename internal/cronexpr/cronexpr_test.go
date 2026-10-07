package cronexpr

import (
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	got, err := Normalize("  */15   1-5 * * MON-FRI ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*/15 1-5 * * MON-FRI" {
		t.Fatalf("normalized = %q", got)
	}
	for _, expression := range []string{"", "0 2 * *", "61 2 * * *", "0 2 * * *\n* * * * *", "@reboot"} {
		if _, err := Normalize(expression); err == nil {
			t.Errorf("Normalize(%q) succeeded", expression)
		}
	}
}

func TestAliases(t *testing.T) {
	tests := map[string]string{
		"@hourly":   "0 * * * *",
		"@daily":    "0 0 * * *",
		"@weekly":   "0 0 * * 0",
		"@monthly":  "0 0 1 * *",
		"@yearly":   "0 0 1 1 *",
		"@annually": "0 0 1 1 *",
	}
	for expression, want := range tests {
		got, err := Normalize(expression)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", expression, err)
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", expression, got, want)
		}
	}
}

func TestDue(t *testing.T) {
	location := time.FixedZone("local", 2*60*60)
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, location)
	dueSuccess := time.Date(2026, 8, 29, 3, 0, 0, 0, location)
	notDueSuccess := time.Date(2026, 8, 30, 3, 0, 0, 0, location)

	if due, err := Due("0 2 * * *", &dueSuccess, now); err != nil || !due {
		t.Fatalf("overdue = %v, error = %v", due, err)
	}
	if due, err := Due("0 2 * * *", &notDueSuccess, now); err != nil || due {
		t.Fatalf("not due = %v, error = %v", due, err)
	}
	if due, err := Due("0 2 * * *", nil, now); err != nil || !due {
		t.Fatalf("never run due = %v, error = %v", due, err)
	}
}

func TestDueImpossibleSchedule(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-24 * time.Hour)
	if due, err := Due("0 0 31 2 *", &lastSuccess, now); err != nil || due {
		t.Fatalf("impossible schedule due = %v, error = %v", due, err)
	}
}

func TestHasRestrictedDayFields(t *testing.T) {
	for _, test := range []struct {
		expression string
		want       bool
	}{
		{"0 0 1 * MON", true},
		{"0 0 * * MON", false},
		{"0 0 ? * MON", false},
		{"0 0 */1 * MON", false},
		{"0 0 1 * */1", false},
		{"0 0 */2 * MON", true},
		{"0 0 1,* * MON", false},
	} {
		got, err := HasRestrictedDayFields(test.expression)
		if err != nil || got != test.want {
			t.Errorf("HasRestrictedDayFields(%q) = %v, %v; want %v", test.expression, got, err, test.want)
		}
	}
	if _, err := HasRestrictedDayFields("invalid"); err == nil {
		t.Fatal("invalid expression accepted")
	}
}
