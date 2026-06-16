package common

import (
	"regexp"
	"strings"
)

var sensitivePattern = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key)\s*[=:]\s*([^\s,;]+)`)

func Redact(value string) string {
	return sensitivePattern.ReplaceAllString(value, "$1=<redacted>")
}

func LooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "password=") ||
		strings.Contains(lower, "passwd=") ||
		strings.Contains(lower, "pwd=") ||
		strings.Contains(lower, "secret=") ||
		strings.Contains(lower, "token=")
}

func CompactOutput(value string, max int) string {
	value = strings.TrimSpace(Redact(value))
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
