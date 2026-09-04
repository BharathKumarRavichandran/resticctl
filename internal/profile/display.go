package profile

import (
	"net/url"
	"strings"
)

const (
	redactedValue    = "[REDACTED]"
	redactedURLValue = "REDACTED"
)

// ResolvedProfile is the effective, secret-minimized profile shown to users.
// Credentials are excluded by Profile's JSON tags.
type ResolvedProfile struct {
	Name string `json:"name"`
	Profile
}

// RedactedResolvedProfile returns a copy suitable for display. Public profile
// values remain visible, while fields commonly used to carry secrets do not.
func RedactedResolvedProfile(value Profile) ResolvedProfile {
	value.Repository = redactRepository(value.Repository)
	value.Monitoring = redactedMonitoring(value.Monitoring)
	return ResolvedProfile{Name: value.Name, Profile: value}
}

func redactedMonitoring(value Monitoring) Monitoring {
	if value.Pushgateway != nil {
		gateway := *value.Pushgateway
		gateway.URL = redactEndpoint(gateway.URL)
		gateway.Headers = redactedHeaders(gateway.Headers)
		value.Pushgateway = &gateway
	}
	value.HTTP = append([]HTTPHook(nil), value.HTTP...)
	for index := range value.HTTP {
		hook := &value.HTTP[index]
		hook.URL = redactEndpoint(hook.URL)
		hook.Headers = redactedHeaders(hook.Headers)
		if hook.Body != "" {
			hook.Body = redactedValue
		}
		if hook.BodyTemplate != "" {
			hook.BodyTemplate = redactedValue
		}
	}
	return value
}

func redactedHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	result := make(map[string]string, len(headers))
	for key := range headers {
		result[key] = redactedValue
	}
	return result
}

func redactEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return redactedValue
	}
	if parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" {
		return raw
	}
	return parsed.Scheme + "://" + parsed.Host + "/" + redactedValue
}

func redactRepository(repository string) string {
	separator := strings.Index(repository, "://")
	if separator < 0 {
		return repository
	}
	schemeStart := strings.LastIndex(repository[:separator], ":") + 1
	parsed, err := url.Parse(repository[schemeStart:])
	if err != nil {
		return redactedValue
	}
	redacted := false
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(username, redactedURLValue)
			redacted = true
		}
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = redactedURLValue
		redacted = true
	}
	if parsed.Fragment != "" {
		parsed.Fragment = redactedURLValue
		redacted = true
	}
	if !redacted {
		return repository
	}
	return repository[:schemeStart] + parsed.String()
}
