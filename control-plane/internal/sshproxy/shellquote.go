package sshproxy

import "strings"

// ShellQuote wraps s in single quotes, escaping embedded single quotes.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
