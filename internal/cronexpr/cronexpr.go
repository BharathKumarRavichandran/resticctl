package cronexpr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func Normalize(expression string) (string, error) {
	if strings.ContainsAny(expression, "\r\n\x00") {
		return "", errors.New("cron expression contains an invalid character")
	}
	expression = expandAlias(strings.TrimSpace(expression))
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return "", errors.New("schedule must be a five-field cron expression")
	}
	normalized := strings.Join(fields, " ")
	if _, err := parser.Parse(normalized); err != nil {
		return "", fmt.Errorf("invalid cron expression: %w", err)
	}
	return normalized, nil
}

func expandAlias(expression string) string {
	alias := strings.TrimPrefix(strings.ToLower(expression), "@")
	switch alias {
	case "hourly":
		return "0 * * * *"
	case "daily":
		return "0 0 * * *"
	case "weekly":
		return "0 0 * * 0"
	case "monthly":
		return "0 0 1 * *"
	case "yearly", "annually":
		return "0 0 1 1 *"
	default:
		return expression
	}
}

func Fields(expression string) ([]string, error) {
	normalized, err := Normalize(expression)
	if err != nil {
		return nil, err
	}
	return strings.Fields(normalized), nil
}

func Due(expression string, lastSuccess *time.Time, now time.Time) (bool, error) {
	normalized, err := Normalize(expression)
	if err != nil {
		return false, err
	}
	if lastSuccess == nil {
		return true, nil
	}
	schedule, err := parser.Parse(normalized)
	if err != nil {
		return false, fmt.Errorf("invalid cron expression: %w", err)
	}
	previous := lastSuccess.In(now.Location())
	return !schedule.Next(previous).After(now), nil
}
