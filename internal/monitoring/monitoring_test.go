package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func TestPrometheusDistinguishesCopyTargets(t *testing.T) {
	finished := time.Unix(100, 0)
	exitCode := 0
	for _, target := range []string{"local", "remote"} {
		metrics := prometheus(runstatus.Status{
			Profile: "example", Command: "copy", TargetType: "copy", TargetName: target,
			FinishedAt: &finished, ExitCode: &exitCode, Statistics: &runstatus.Statistics{FilesNew: 1},
		})
		want := `target_type="copy",target_name="` + target + `"`
		count := 0
		for _, line := range strings.Split(metrics, "\n") {
			if strings.HasPrefix(line, "resticctl_") {
				count++
				if !strings.Contains(line, want) {
					t.Errorf("metric lacks copy target identity: %s", line)
				}
			}
		}
		if count != 11 {
			t.Errorf("metric count = %d, want 11", count)
		}
	}
}

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type closingTransport struct {
	roundTripFunc
	closed bool
}

func (transport *closingTransport) CloseIdleConnections() { transport.closed = true }

func TestHTTPDeliveriesCloseIdleConnections(t *testing.T) {
	for _, fail := range []bool{false, true} {
		for _, kind := range []string{"hook", "pushgateway"} {
			t.Run(fmt.Sprintf("%s/fail=%v", kind, fail), func(t *testing.T) {
				transport := &closingTransport{roundTripFunc: func(*http.Request) (*http.Response, error) {
					if fail {
						return nil, errors.New("delivery failed")
					}
					return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
				}}
				original := newHTTPClient
				newHTTPClient = func(time.Duration, string) (*http.Client, error) {
					return &http.Client{Transport: transport}, nil
				}
				t.Cleanup(func() { newHTTPClient = original })
				var err error
				if kind == "hook" {
					err = sendHTTP(context.Background(), profile.HTTPHook{URL: "https://monitor.example", Method: http.MethodPost}, Event{})
				} else {
					err = push(context.Background(), profile.Pushgateway{URL: "https://push.example", Job: "backups"}, runstatus.Status{})
				}
				if (err != nil) != fail {
					t.Fatalf("delivery error = %v, want failure = %v", err, fail)
				}
				if !transport.closed {
					t.Fatal("idle connections were not closed")
				}
			})
		}
	}
}

func TestPushgatewayCopyGroupsRetainSeparateResults(t *testing.T) {
	groups := make(map[string]string)
	var mu sync.Mutex
	useHTTPFake(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut {
			t.Errorf("method = %s", request.Method)
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		groups[request.URL.Path] = string(data)
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})
	gateway := profile.Pushgateway{URL: "https://push.example", Job: "backups", Labels: map[string]string{"site": "test", "target_name": "override"}}
	for _, target := range []string{"local", "remote", "local"} {
		status := runstatus.Status{Profile: "example", Command: "copy", TargetType: "copy", TargetName: target, State: "failed"}
		if target == "local" {
			status.State = "succeeded"
		}
		if err := push(context.Background(), gateway, status); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for _, target := range []string{"local", "remote"} {
		go func() {
			results <- push(context.Background(), gateway, runstatus.Status{
				Profile: "example", Command: "copy", TargetType: "copy", TargetName: target, State: "failed",
			})
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if len(groups) != 2 {
		t.Fatalf("group count = %d", len(groups))
	}
	for _, target := range []string{"local", "remote"} {
		path := "/metrics/job/backups/command/copy/profile/example/site/test/target_name/" + target + "/target_type/copy"
		status := runstatus.Status{Profile: "example", Command: "copy", TargetType: "copy", TargetName: target}
		if !strings.Contains(groups[path], "resticctl_run_success{"+metricLabels(status)+"} 0\n") {
			t.Fatalf("missing group %s: %v", path, groups)
		}
	}
	if gateway.Labels["target_name"] != "override" {
		t.Fatal("configured labels were mutated")
	}
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
	sort.Strings(paths)
	if len(paths) != 2 || paths[0] != "/event" || paths[1] != "/metrics/job/backups/command/backup/profile/example/site/test" {
		t.Fatalf("request paths = %v", paths)
	}
}

func TestReporterFailuresDoNotExposeSensitiveHeaders(t *testing.T) {
	useHTTPFake(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: make(http.Header)}, nil
	})
	var diagnostics strings.Builder
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{HTTP: []profile.HTTPHook{{Name: "healthcheck", URL: "https://monitor.example", Method: http.MethodPost, Phases: []string{"send-finally"}, Headers: map[string]string{"Authorization": "secret"}}}}}, nil, &diagnostics)
	err := reporter.Report(context.Background(), "send-finally", runstatus.Status{Profile: "example", Action: "backup"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
	if got := diagnostics.String(); !strings.Contains(got, "healthcheck failed") || strings.Contains(got, "secret") || strings.Contains(got, "https://") {
		t.Fatalf("diagnostic = %q", got)
	}
}

func TestReporterDeliversIndependentTargetsInParallel(t *testing.T) {
	var started atomic.Int32
	ready := make(chan struct{})
	useHTTPFake(t, func(*http.Request) (*http.Response, error) {
		if started.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Header: make(http.Header)}, nil
		case <-time.After(time.Second):
			return nil, context.DeadlineExceeded
		}
	})
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{HTTP: []profile.HTTPHook{
		{Name: "one", URL: "https://one.example", Method: http.MethodPost, Phases: []string{"send-before"}},
		{Name: "two", URL: "https://two.example", Method: http.MethodPost, Phases: []string{"send-before"}},
	}}}, nil, io.Discard)
	if err := reporter.Report(context.Background(), "send-before", runstatus.Status{Action: "backup"}); err != nil {
		t.Fatal(err)
	}
	if started.Load() != 2 {
		t.Fatalf("started deliveries = %d", started.Load())
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

func TestPushgatewaySeparatesProfilesAndCommands(t *testing.T) {
	paths := map[string]bool{}
	useHTTPFake(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut {
			t.Fatalf("method=%s", request.Method)
		}
		paths[request.URL.Path] = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	gateway := profile.Pushgateway{URL: "http://gateway", Job: "backups", Labels: map[string]string{"profile": "override", "command": "override"}}
	for _, name := range []string{"home", "work"} {
		for _, command := range []string{"backup", "check"} {
			if err := push(context.Background(), gateway, runstatus.Status{Profile: name, Command: command}); err != nil {
				t.Fatal(err)
			}
			want := "/metrics/job/backups/command/" + command + "/profile/" + name
			if !paths[want] {
				t.Fatalf("missing grouping %s: %v", want, paths)
			}
		}
	}
	if len(paths) != 4 {
		t.Fatalf("groupings=%v", paths)
	}
}

func TestActionStatusExportsRetainIndependentResults(t *testing.T) {
	for _, test := range []struct{ filename, prefix, suffix string }{
		{"latest.json", "latest-", ".json"},
		{"custom.status.json", "custom.status-", ".json"},
		{"status", "status-", ""},
	} {
		t.Run(test.filename, func(t *testing.T) {
			directory := t.TempDir()
			shared := filepath.Join(directory, test.filename)
			reporter := New(profile.Profile{Monitoring: profile.Monitoring{StatusFile: shared}}, nil)
			assertStatus := func(path, action, state string) {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var status runstatus.Status
				if err := json.Unmarshal(data, &status); err != nil || status.Action != action || status.State != state {
					t.Fatalf("%s: status=%+v error=%v", path, status, err)
				}
			}
			for _, action := range []string{"backup", "forget", "check", "prune", "copy"} {
				state := "succeeded"
				if action == "backup" {
					state = "failed"
				}
				status := runstatus.Status{Profile: "example", Action: action, Command: action, State: state}
				if err := reporter.Report(context.Background(), "send-finally", status); err != nil {
					t.Fatal(err)
				}
			}
			for _, action := range []string{"backup", "forget", "check", "prune", "copy"} {
				state := "succeeded"
				if action == "backup" {
					state = "failed"
				}
				assertStatus(filepath.Join(directory, test.prefix+action+test.suffix), action, state)
			}
			assertStatus(shared, "copy", "succeeded")
			if err := reporter.Report(context.Background(), "send-finally", runstatus.Status{Action: "backup", State: "succeeded"}); err != nil {
				t.Fatal(err)
			}
			assertStatus(shared, "backup", "succeeded")
			assertStatus(filepath.Join(directory, test.prefix+"backup"+test.suffix), "backup", "succeeded")
			assertStatus(filepath.Join(directory, test.prefix+"forget"+test.suffix), "forget", "succeeded")
		})
	}
}

func TestActionStatusExportSurvivesSharedExportFailure(t *testing.T) {
	directory := t.TempDir()
	shared := filepath.Join(directory, "latest.json")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{StatusFile: shared}}, nil)
	if err := reporter.export(context.Background(), runstatus.Status{Action: "forget"}); err == nil {
		t.Fatal("expected shared export error")
	}
	if _, err := os.Stat(filepath.Join(directory, "latest-forget.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSharedStatusExportSurvivesActionExportFailure(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "latest-forget.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(directory, "latest.json")
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{StatusFile: shared}}, nil)
	if err := reporter.export(context.Background(), runstatus.Status{Action: "forget"}); err == nil {
		t.Fatal("expected action export error")
	}
	data, err := os.ReadFile(shared)
	if err != nil || !strings.Contains(string(data), `"action": "forget"`) {
		t.Fatalf("shared export=%s error=%v", data, err)
	}
}

func TestActionStatusExportsDisabled(t *testing.T) {
	directory := t.TempDir()
	reporter := New(profile.Profile{Monitoring: profile.Monitoring{
		PrometheusTextfile: filepath.Join(directory, "metrics.prom"),
	}}, nil)
	if err := reporter.Report(context.Background(), "send-finally", runstatus.Status{Action: "backup"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 || entries[0].Name() != "metrics.prom" || entries[1].Name() != "metrics.prom.lock" {
		t.Fatalf("disabled JSON exports created unexpected files: %v error=%v", entries, err)
	}
}
