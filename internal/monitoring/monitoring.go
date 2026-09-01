// Package monitoring implements best-effort, secret-minimizing run telemetry.
package monitoring

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/securefile"
)

const defaultTimeout = 10 * time.Second

var newHTTPClient = httpClient
var dialTimeout = net.DialTimeout

// Event is intentionally free of command output, repository locations,
// credentials, and local source paths.
type Event struct {
	Phase  string           `json:"phase"`
	Status runstatus.Status `json:"status"`
}

type Reporter struct {
	profile profile.Profile
	console io.Writer
}

func New(backupProfile profile.Profile, console io.Writer) *Reporter {
	return &Reporter{profile: backupProfile, console: console}
}

// Report performs all configured deliveries and returns their joined errors.
// Callers deliberately treat this result as non-fatal to the protected action.
func (reporter *Reporter) Report(ctx context.Context, phase string, status runstatus.Status) error {
	event := Event{Phase: phase, Status: status}
	var deliveryErrors []error
	for _, hook := range reporter.profile.Monitoring.HTTP {
		if matches(hook, phase, status.Action) {
			if err := sendHTTP(ctx, hook, event); err != nil {
				deliveryErrors = append(deliveryErrors, err)
			}
		}
	}
	if phase == "send-finally" {
		if err := reporter.export(status); err != nil {
			deliveryErrors = append(deliveryErrors, err)
		}
		if gateway := reporter.profile.Monitoring.Pushgateway; gateway != nil {
			if err := push(ctx, *gateway, status); err != nil {
				deliveryErrors = append(deliveryErrors, err)
			}
		}
	}
	if err := reporter.log(event); err != nil {
		deliveryErrors = append(deliveryErrors, err)
	}
	return errors.Join(deliveryErrors...)
}

func matches(hook profile.HTTPHook, phase, action string) bool {
	if len(hook.Actions) > 0 && !contains(hook.Actions, action) {
		return false
	}
	phases := hook.Phases
	if len(phases) == 0 {
		phases = []string{"send-finally"}
	}
	return contains(phases, phase)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sendHTTP(ctx context.Context, hook profile.HTTPHook, event Event) error {
	body := hook.Body
	if hook.BodyTemplate != "" {
		parsed, err := template.New("body").Option("missingkey=error").Parse(hook.BodyTemplate)
		if err != nil {
			return fmt.Errorf("monitoring target %q template: %w", hook.Name, err)
		}
		var rendered bytes.Buffer
		if err := parsed.Execute(&rendered, event); err != nil {
			return fmt.Errorf("monitoring target %q template: %w", hook.Name, err)
		}
		body = rendered.String()
	} else if body == "" {
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		body = string(encoded)
	}
	timeout := parseTimeout(hook.Timeout)
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, hook.Method, hook.URL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("monitoring target %q: %w", hook.Name, err)
	}
	for key, value := range hook.Headers {
		request.Header.Set(key, value)
	}
	if request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	client, err := newHTTPClient(timeout, hook.CAFile)
	if err != nil {
		return fmt.Errorf("monitoring target %q: %w", hook.Name, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("monitoring target %q delivery: %w", hook.Name, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("monitoring target %q returned HTTP %d", hook.Name, response.StatusCode)
	}
	return nil
}

func (reporter *Reporter) export(status runstatus.Status) error {
	monitoring := reporter.profile.Monitoring
	var exportErrors []error
	if monitoring.StatusFile != "" {
		data, err := json.MarshalIndent(status, "", "  ")
		if err == nil {
			data = append(data, '\n')
			err = writeAtomic(monitoring.StatusFile, data)
		}
		if err != nil {
			exportErrors = append(exportErrors, fmt.Errorf("JSON status export: %w", err))
		}
	}
	if monitoring.PrometheusTextfile != "" {
		if err := writeAtomic(monitoring.PrometheusTextfile, []byte(prometheus(status))); err != nil {
			exportErrors = append(exportErrors, fmt.Errorf("Prometheus textfile export: %w", err))
		}
	}
	return errors.Join(exportErrors...)
}

func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return securefile.WriteAtomic(path, data)
}

func prometheus(status runstatus.Status) string {
	labels := `profile="` + metricEscape(status.Profile) + `",command="` + metricEscape(status.Command) + `"`
	success, warning := 0, 0
	if status.State == "succeeded" || status.State == "warning" {
		success = 1
	}
	if status.Warning || status.State == "warning" {
		warning = 1
	}
	var result strings.Builder
	fmt.Fprintf(&result, "# HELP resticctl_run_success Whether the latest run completed successfully.\n# TYPE resticctl_run_success gauge\nresticctl_run_success{%s} %d\n", labels, success)
	fmt.Fprintf(&result, "# HELP resticctl_run_warning Whether Restic reported a non-fatal warning.\n# TYPE resticctl_run_warning gauge\nresticctl_run_warning{%s} %d\n", labels, warning)
	fmt.Fprintf(&result, "# TYPE resticctl_run_duration_seconds gauge\nresticctl_run_duration_seconds{%s} %.3f\n", labels, float64(status.DurationMS)/1000)
	if status.FinishedAt != nil {
		fmt.Fprintf(&result, "# TYPE resticctl_run_finished_timestamp_seconds gauge\nresticctl_run_finished_timestamp_seconds{%s} %d\n", labels, status.FinishedAt.Unix())
	}
	if status.ExitCode != nil {
		fmt.Fprintf(&result, "# TYPE resticctl_run_exit_code gauge\nresticctl_run_exit_code{%s} %d\n", labels, *status.ExitCode)
	}
	if statistics := status.Statistics; statistics != nil {
		values := map[string]uint64{"files_new": statistics.FilesNew, "files_changed": statistics.FilesChanged, "files_unmodified": statistics.FilesUnmodified, "data_added_bytes": statistics.DataAddedBytes, "total_files_processed": statistics.TotalFilesProcessed, "total_bytes_processed": statistics.TotalBytesProcessed}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&result, "resticctl_backup_%s{%s} %d\n", key, labels, values[key])
		}
	}
	return result.String()
}

func metricEscape(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", `"`, `\"`).Replace(value)
}

func push(ctx context.Context, gateway profile.Pushgateway, status runstatus.Status) error {
	endpoint := strings.TrimRight(gateway.URL, "/") + "/metrics/job/" + url.PathEscape(gateway.Job)
	labels := make([]string, 0, len(gateway.Labels))
	for key := range gateway.Labels {
		labels = append(labels, key)
	}
	sort.Strings(labels)
	for _, key := range labels {
		endpoint += "/" + url.PathEscape(key) + "/" + url.PathEscape(gateway.Labels[key])
	}
	timeout := parseTimeout(gateway.Timeout)
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPut, endpoint, strings.NewReader(prometheus(status)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "text/plain; version=0.0.4")
	for key, value := range gateway.Headers {
		request.Header.Set(key, value)
	}
	client, err := newHTTPClient(timeout, gateway.CAFile)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Pushgateway delivery: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Pushgateway returned HTTP %d", response.StatusCode)
	}
	return nil
}

func httpClient(timeout time.Duration, caFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := readBounded(caFile, 10<<20)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("custom CA file contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	return &http.Client{
		Timeout: timeout, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return data, nil
}

func parseTimeout(value string) time.Duration {
	if value == "" {
		return defaultTimeout
	}
	duration, _ := time.ParseDuration(value)
	return duration
}

func (reporter *Reporter) log(event Event) error {
	if len(reporter.profile.Monitoring.Logs) == 0 {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	var logErrors []error
	for _, destination := range reporter.profile.Monitoring.Logs {
		err = nil
		switch destination.Type {
		case "console":
			if reporter.console != nil {
				_, err = reporter.console.Write(data)
			}
		case "file":
			err = appendPrivate(destination.Path, data)
		case "temporary-file":
			var file *os.File
			file, err = os.CreateTemp("", "resticctl-log-*.jsonl")
			if err == nil {
				err = file.Chmod(0o600)
				if err == nil {
					_, err = file.Write(data)
				}
				err = errors.Join(err, file.Close())
			}
		case "local-syslog":
			err = sendLocalSyslog(data)
		case "remote-syslog":
			err = sendSyslog(destination.Network, destination.Address, data)
		}
		if err != nil {
			logErrors = append(logErrors, err)
		}
	}
	return errors.Join(logErrors...)
}

func appendPrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("log path must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

func sendLocalSyslog(data []byte) error {
	for _, address := range []string{"/dev/log", "/var/run/syslog"} {
		if err := sendSyslog("unixgram", address, data); err == nil {
			return nil
		}
	}
	return errors.New("local syslog socket is unavailable")
}

func sendSyslog(network, address string, data []byte) error {
	connection, err := dialTimeout(network, address, defaultTimeout)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetWriteDeadline(time.Now().Add(defaultTimeout))
	message := "<14>1 " + time.Now().UTC().Format(time.RFC3339) + " - resticctl - - - " + strconv.QuoteToASCII(strings.TrimSpace(string(data))) + "\n"
	_, err = io.WriteString(connection, message)
	return err
}
