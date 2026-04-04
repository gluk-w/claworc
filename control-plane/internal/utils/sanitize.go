package utils

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

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

// ValidateAndBuildURL validates a user-provided base URL, ensures it uses http(s)
// scheme and has a host, then appends the given path suffix. Returns an error if
// validation fails. The returned URL string is constructed from validated parts
// using fmt.Sprintf to ensure the output is a well-formed URL.
func ValidateAndBuildURL(rawBaseURL, pathSuffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(rawBaseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("invalid URL")
	}
	scheme := parsed.Scheme
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("URL scheme must be http or https")
	}
	host := parsed.Host
	if host == "" {
		return "", fmt.Errorf("URL must have a host")
	}
	// Validate host contains no path separators or suspicious characters
	if strings.ContainsAny(host, "/@\\") {
		return "", fmt.Errorf("invalid host")
	}
	pathPart := parsed.Path + pathSuffix
	// Reconstruct URL from validated, individually extracted components
	return fmt.Sprintf("%s://%s%s", scheme, host, pathPart), nil
}

// ValidateExternalURL builds a safe URL using ValidateAndBuildURL and then
// resolves the hostname to ensure it does not point to a private, loopback, or
// link-local IP address (SSRF protection). Returns the validated URL string or
// an error describing why the URL was rejected.
func ValidateExternalURL(rawBaseURL, pathSuffix string) (string, error) {
	safeURL, err := ValidateAndBuildURL(rawBaseURL, pathSuffix)
	if err != nil {
		return "", err
	}

	parsed, _ := url.Parse(safeURL) // already validated above
	hostname := parsed.Hostname()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return "", fmt.Errorf("DNS resolution failed for %s: %w", hostname, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no DNS records for %s", hostname)
	}

	for _, ip := range ips {
		if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() ||
			ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() {
			return "", fmt.Errorf("URL resolves to a private/internal address (%s)", ip.IP)
		}
	}

	return safeURL, nil
}

// SanitizePath validates a path component to prevent directory traversal.
// It returns the base name only, stripping any directory components.
func SanitizePath(p string) string {
	// Remove all path separators and parent references
	cleaned := strings.ReplaceAll(p, "..", "")
	cleaned = strings.ReplaceAll(cleaned, "/", "")
	cleaned = strings.ReplaceAll(cleaned, "\\", "")
	if cleaned == "" {
		return "invalid"
	}
	return cleaned
}
