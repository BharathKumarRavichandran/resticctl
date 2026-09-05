package profile

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

var monitoringActions = map[string]bool{"backup": true, "check": true, "forget": true, "prune": true, "copy": true}
var monitoringPhases = map[string]bool{"send-before": true, "send-after": true, "send-after-fail": true, "send-finally": true, "warning": true}

func validateMonitoring(p *Profile, base string) error {
	m := &p.Monitoring
	if m.HistoryLimit < 0 {
		return errors.New("monitoring.history_limit must not be negative")
	}
	if m.HistoryLimit == 0 {
		m.HistoryLimit = 100
	}
	if m.WarningPolicy == "" {
		m.WarningPolicy = "failure"
	}
	if m.WarningPolicy != "failure" && m.WarningPolicy != "warning" && m.WarningPolicy != "success" {
		return errors.New("monitoring.warning_policy must be failure, warning, or success")
	}
	var err error
	if m.StatusFile, err = optionalMonitoringPath(m.StatusFile, base); err != nil {
		return fmt.Errorf("invalid monitoring.status_file: %w", err)
	}
	if m.PrometheusTextfile, err = optionalMonitoringPath(m.PrometheusTextfile, base); err != nil {
		return fmt.Errorf("invalid monitoring.prometheus_textfile: %w", err)
	}
	if m.Pushgateway != nil {
		gateway := m.Pushgateway
		if err := validateHTTPEndpoint(gateway.URL); err != nil {
			return fmt.Errorf("invalid monitoring.pushgateway.url: %w", err)
		}
		if gateway.Job == "" {
			gateway.Job = "resticctl"
		}
		if !isPortableName(gateway.Job) {
			return fmt.Errorf("invalid monitoring.pushgateway.job: %s", gateway.Job)
		}
		if err := validateMonitoringDuration(gateway.Timeout); err != nil {
			return fmt.Errorf("invalid monitoring.pushgateway.timeout: %w", err)
		}
		if err := validateLabels(gateway.Labels); err != nil {
			return fmt.Errorf("invalid monitoring.pushgateway.labels: %w", err)
		}
		if gateway.CAFile, err = optionalInputMonitoringPath(gateway.CAFile, base); err != nil {
			return fmt.Errorf("invalid monitoring.pushgateway.ca_file: %w", err)
		}
		if err := validateHeaders(gateway.Headers); err != nil {
			return fmt.Errorf("invalid monitoring.pushgateway.headers: %w", err)
		}
	}
	seen := map[string]bool{}
	for i := range m.HTTP {
		hook := &m.HTTP[i]
		if err := validateHTTPEndpoint(hook.URL); err != nil {
			return fmt.Errorf("invalid monitoring.http[%d].url: %w", i, err)
		}
		if hook.Name != "" {
			key := strings.ToLower(hook.Name)
			if !isPortableName(hook.Name) || seen[key] {
				return fmt.Errorf("invalid or duplicate monitoring HTTP target name %q", hook.Name)
			}
			seen[key] = true
		}
		if hook.Method == "" {
			hook.Method = "POST"
		}
		hook.Method = strings.ToUpper(hook.Method)
		if hook.Method != "GET" && hook.Method != "POST" && hook.Method != "PUT" && hook.Method != "PATCH" {
			return fmt.Errorf("monitoring.http[%d].method is unsupported: %s", i, hook.Method)
		}
		if hook.Body != "" && hook.BodyTemplate != "" {
			return fmt.Errorf("monitoring.http[%d] must not set both body and body_template", i)
		}
		if hook.BodyTemplate != "" {
			if _, err := template.New("body").Option("missingkey=error").Parse(hook.BodyTemplate); err != nil {
				return fmt.Errorf("invalid monitoring.http[%d].body_template: %w", i, err)
			}
		}
		if err := validateMonitoringDuration(hook.Timeout); err != nil {
			return fmt.Errorf("invalid monitoring.http[%d].timeout: %w", i, err)
		}
		if err := validateHeaders(hook.Headers); err != nil {
			return fmt.Errorf("invalid monitoring.http[%d].headers: %w", i, err)
		}
		for _, action := range hook.Actions {
			if !monitoringActions[action] {
				return fmt.Errorf("monitoring.http[%d] has unsupported action %q", i, action)
			}
		}
		for _, phase := range hook.Phases {
			if !monitoringPhases[phase] {
				return fmt.Errorf("monitoring.http[%d] has unsupported phase %q", i, phase)
			}
		}
		if hook.CAFile, err = optionalInputMonitoringPath(hook.CAFile, base); err != nil {
			return fmt.Errorf("invalid monitoring.http[%d].ca_file: %w", i, err)
		}
	}
	for i := range m.Logs {
		destination := &m.Logs[i]
		switch destination.Type {
		case "console", "temporary-file", "local-syslog":
		case "file":
			if destination.Path == "" {
				return fmt.Errorf("monitoring.logs[%d].path is required", i)
			}
			destination.Path, err = optionalMonitoringPath(destination.Path, base)
			if err != nil {
				return fmt.Errorf("invalid monitoring.logs[%d].path: %w", i, err)
			}
		case "remote-syslog":
			if destination.Address == "" || strings.ContainsRune(destination.Address, 0) {
				return fmt.Errorf("monitoring.logs[%d].address is required", i)
			}
			if destination.Network == "" {
				destination.Network = "udp"
			}
			if destination.Network != "udp" && destination.Network != "tcp" {
				return fmt.Errorf("monitoring.logs[%d].network must be udp or tcp", i)
			}
		default:
			return fmt.Errorf("monitoring.logs[%d].type is unsupported: %s", i, destination.Type)
		}
	}
	sensitive := []string{filepath.Join(base, p.Name+".json"), p.CredentialsFile, p.PrivateFile, p.Credentials.Password.File}
	for _, credential := range p.Credentials.DatabaseCredentials {
		sensitive = append(sensitive, credential.Password.File)
	}
	for _, database := range p.MongoDBDatabases {
		sensitive = append(sensitive, database.ConfigFile)
	}
	outputs := []string{m.StatusFile, m.PrometheusTextfile}
	for _, destination := range m.Logs {
		if destination.Type == "file" {
			outputs = append(outputs, destination.Path)
		}
	}
	for index, output := range outputs {
		if output == "" {
			continue
		}
		for _, protected := range sensitive {
			if protected != "" && strings.EqualFold(filepath.Clean(output), filepath.Clean(protected)) {
				return fmt.Errorf("monitoring output must not overwrite a sensitive configuration file: %s", output)
			}
		}
		for _, earlier := range outputs[:index] {
			if earlier != "" && strings.EqualFold(filepath.Clean(output), filepath.Clean(earlier)) {
				return fmt.Errorf("monitoring outputs must use distinct paths: %s", output)
			}
		}
	}
	return nil
}

func validateHTTPEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("must be an http(s) URL without embedded credentials")
	}
	return nil
}

func validateMonitoringDuration(value string) error {
	if value == "" {
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return errors.New("must be a positive duration")
	}
	return nil
}

func validateLabels(labels map[string]string) error {
	for key, value := range labels {
		if key == "" || strings.ContainsAny(key, "{}=,\x00") || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid label %q", key)
		}
	}
	return nil
}

func validateHeaders(headers map[string]string) error {
	for key, value := range headers {
		if key == "" || strings.ContainsAny(key, "\r\n\x00") || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("invalid header %q", key)
		}
	}
	return nil
}

func optionalMonitoringPath(value, base string) (string, error) {
	if value == "" {
		return "", nil
	}
	return expandPath(value, base)
}

func optionalInputMonitoringPath(value, base string) (string, error) {
	path, err := optionalMonitoringPath(value, base)
	if err != nil || path == "" {
		return path, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot inspect custom CA file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("custom CA must be a regular file")
	}
	if info.Size() > 10<<20 {
		return "", errors.New("custom CA file is too large")
	}
	return path, nil
}
