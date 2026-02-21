package sshmanager

import (
	"fmt"
	"net"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// New event type for IP restriction violations.
const (
	EventIPRestricted EventType = "ip_restricted"
)

// ParseAllowedIPs parses a comma-separated list of IPs and CIDR ranges.
// Returns a list of *net.IPNet entries. Single IPs are converted to /32 (IPv4)
// or /128 (IPv6) CIDRs. Empty input returns nil (allow-all).
func ParseAllowedIPs(allowList string) ([]*net.IPNet, error) {
	allowList = strings.TrimSpace(allowList)
	if allowList == "" {
		return nil, nil
	}

	parts := strings.Split(allowList, ",")
	var networks []*net.IPNet

	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}

		// Try parsing as CIDR first
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", entry, err)
			}
			networks = append(networks, network)
			continue
		}

		// Parse as single IP
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address %q", entry)
		}

		// Convert to CIDR
		var mask net.IPMask
		if ip.To4() != nil {
			mask = net.CIDRMask(32, 32)
		} else {
			mask = net.CIDRMask(128, 128)
		}
		networks = append(networks, &net.IPNet{IP: ip.Mask(mask), Mask: mask})
	}

	return networks, nil
}

// CheckIPAllowed verifies that the given source IP is allowed by the allow list.
// If allowList is empty, all IPs are allowed. Returns nil if allowed, or an
// error describing why the IP was blocked.
func CheckIPAllowed(sourceIP, allowList string) error {
	networks, err := ParseAllowedIPs(allowList)
	if err != nil {
		return fmt.Errorf("invalid allow list: %w", err)
	}

	// Empty allow list means allow all
	if len(networks) == 0 {
		return nil
	}

	ip := net.ParseIP(strings.TrimSpace(sourceIP))
	if ip == nil {
		return fmt.Errorf("connection blocked: could not parse source IP %q", logutil.SanitizeForLog(sourceIP))
	}

	for _, network := range networks {
		if network.Contains(ip) {
			return nil
		}
	}

	return fmt.Errorf("connection blocked: source IP %s is not in the allowed list",
		logutil.SanitizeForLog(sourceIP))
}

// NormalizeAllowList validates and normalizes a comma-separated IP/CIDR list.
// Returns the cleaned string or an error if any entry is invalid.
func NormalizeAllowList(allowList string) (string, error) {
	allowList = strings.TrimSpace(allowList)
	if allowList == "" {
		return "", nil
	}

	parts := strings.Split(allowList, ",")
	var normalized []string

	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}

		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return "", fmt.Errorf("invalid CIDR %q: %w", entry, err)
			}
			normalized = append(normalized, network.String())
		} else {
			ip := net.ParseIP(entry)
			if ip == nil {
				return "", fmt.Errorf("invalid IP address %q", entry)
			}
			normalized = append(normalized, ip.String())
		}
	}

	return strings.Join(normalized, ", "), nil
}
