package monitoring

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func useHTTPFake(t *testing.T, function roundTripFunc) {
	t.Helper()
	original := newHTTPClient
	newHTTPClient = func(time.Duration, string) (*http.Client, error) {
		return &http.Client{Transport: function}, nil
	}
	t.Cleanup(func() { newHTTPClient = original })
}

func TestReporterDeliversHooksAndExports(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	useHTTPFake(t, func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, request.URL.Path)
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	directory := t.TempDir()
	finished := time.Unix(100, 0).UTC()
	status := runstatus.Status{Profile: "example", Action: "backup", Command: "backup", State: "succeeded", StartedAt: finished.Add(-time.Second), FinishedAt: &finished, DurationMS: 1000}
	reporter := New(profile.Profile{Name: "example", Monitoring: profile.Monitoring{
		StatusFile: filepath.Join(directory, "status.json"), PrometheusTextfile: filepath.Join(directory, "status.prom"),
		HTTP:        []profile.HTTPHook{{Name: "events", URL: "https://monitor.example/event", Method: http.MethodPost, Phases: []string{"send-finally"}}},
		Pushgateway: &profile.Pushgateway{URL: "https://push.example", Job: "backups", Labels: map[string]string{"site": "test"}},
	}}, nil)
	if err := reporter.Report(context.Background(), "send-finally", status); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exported runstatus.Status
	if err := json.Unmarshal(data, &exported); err != nil || exported.Profile != "example" {
		t.Fatalf("exported status = %#v, error = %v", exported, err)
	}
	metrics, err := os.ReadFile(filepath.Join(directory, "status.prom"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metrics), `resticctl_run_success{profile="example",command="backup"} 1`) {
		t.Fatalf("metrics = %s", metrics)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/event" || paths[1] != "/metrics/job/backups/site/test" {
		t.Fatalf("request paths = %v", paths)
	}
}

func TestReporterFailuresDoNotExposeSensitiveHeaders(t *testing.T) {
	useHTTPFake(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{HTTP: []profile.HTTPHook{{URL: "https://monitor.example", Method: http.MethodPost, Phases: []string{"send-finally"}, Headers: map[string]string{"Authorization": "secret"}}}}}, nil)
	err := reporter.Report(context.Background(), "send-finally", runstatus.Status{Profile: "example", Action: "backup"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteSyslogUsesConfiguredTransport(t *testing.T) {
	client, server := net.Pipe()
	original := dialTimeout
	dialTimeout = func(network, address string, _ time.Duration) (net.Conn, error) {
		if network != "tcp" || address != "logs.example:514" {
			t.Fatalf("dial = %s %s", network, address)
		}
		return client, nil
	}
	t.Cleanup(func() { dialTimeout = original; _ = server.Close() })
	received := make(chan string, 1)
	go func() { data, _ := io.ReadAll(server); received <- string(data) }()
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{Logs: []profile.LogDestination{{Type: "remote-syslog", Network: "tcp", Address: "logs.example:514"}}}}, nil)
	if err := reporter.Report(context.Background(), "send-finally", runstatus.Status{Profile: "example", Action: "backup", State: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if message := <-received; !strings.Contains(message, `\"profile\":\"example\"`) {
		t.Fatalf("syslog message = %s", message)
	}
}
