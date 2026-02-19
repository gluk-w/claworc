package logutil

import "strings"

// SanitizeForLog removes newlines and control characters from user-provided
// strings to prevent log injection attacks where attackers could inject
// fake log entries by including newline characters.
func SanitizeForLog(s string) string {
	// Replace all newlines with spaces
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// Replace tabs with spaces
	s = strings.ReplaceAll(s, "\t", " ")
	// Remove other control characters (ASCII 0-31 except space)
	var result strings.Builder
	result.Grow(len(s))
	for _, r := range s {
		if r >= 32 || r == ' ' {
			result.WriteRune(r)
		}
	}
	return result.String()
}
