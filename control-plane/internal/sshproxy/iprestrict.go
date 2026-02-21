// iprestrict.go implements SSH connection source IP restrictions for the sshproxy package.
//
// Each instance can optionally define an AllowedSourceIPs whitelist (comma-separated
// IPs and CIDR ranges). Before the control plane establishes an SSH connection to an
// instance, it resolves its own outbound IP for that target and verifies it falls within
// the whitelist. If the whitelist is empty, all connections are allowed.
//
// This provides defense-in-depth by ensuring SSH connections only originate from
// approved network ranges, useful in multi-network or segmented environments.
package sshproxy

import (
	"fmt"
	"log"
	"net"
	"strings"
)

// ErrIPRestricted is returned when a connection is blocked by IP restrictions.
type ErrIPRestricted struct {
	InstanceID uint
	SourceIP   string
	Reason     string
}

func (e *ErrIPRestricted) Error() string {
	return fmt.Sprintf("SSH connection to instance %d blocked: source IP %s not in allowed list (%s)",
		e.InstanceID, e.SourceIP, e.Reason)
}

// IPRestriction holds parsed IP addresses and CIDR ranges for whitelist checking.
type IPRestriction struct {
	// CIDRs contains parsed CIDR network ranges (e.g., 10.0.0.0/8).
	CIDRs []*net.IPNet

	// IPs contains individual IP addresses (parsed from entries without a mask).
	IPs []net.IP

	// Raw is the original comma-separated string for display/logging.
	Raw string
}

// ParseIPRestrictions parses a comma-separated list of IP addresses and CIDR ranges.
// Each entry can be:
//   - An individual IP address (e.g., "10.0.0.1", "::1")
//   - A CIDR range (e.g., "10.0.0.0/8", "192.168.0.0/16", "fd00::/8")
//
// Returns nil if the input is empty (meaning no restrictions).
// Returns an error if any entry is malformed.
func ParseIPRestrictions(csv string) (*IPRestriction, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}

	r := &IPRestriction{Raw: csv}

	entries := strings.Split(csv, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Try parsing as CIDR first.
		if strings.Contains(entry, "/") {
			_, ipNet, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", entry, err)
			}
			r.CIDRs = append(r.CIDRs, ipNet)
			continue
		}

		// Parse as individual IP.
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address %q", entry)
		}
		r.IPs = append(r.IPs, ip)
	}

	// If all entries were empty strings after trimming, treat as no restriction.
	if len(r.CIDRs) == 0 && len(r.IPs) == 0 {
		return nil, nil
	}

	return r, nil
}

// IsAllowed checks whether the given IP is permitted by this restriction.
// Returns true if the IP matches any CIDR range or individual IP in the whitelist.
func (r *IPRestriction) IsAllowed(ip net.IP) bool {
	if r == nil {
		return true // No restrictions.
	}

	for _, cidr := range r.CIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}

	for _, allowed := range r.IPs {
		if allowed.Equal(ip) {
			return true
		}
	}

	return false
}

// GetOutboundIP determines the local (source) IP address that would be used
// to connect to the given target host:port. It does this by creating a UDP
// "connection" (no actual packets are sent) and reading the local address.
func GetOutboundIP(targetHost string, targetPort int) (net.IP, error) {
	addr := net.JoinHostPort(targetHost, fmt.Sprintf("%d", targetPort))
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("determine outbound IP for %s: %w", addr, err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP, nil
}

// CheckSourceIPAllowed verifies that the control plane's outbound IP for the
// given target falls within the instance's allowed source IP whitelist.
//
// If allowedSourceIPs is empty, all connections are allowed.
// If the outbound IP cannot be determined or is not in the whitelist, an error is returned.
// Blocked attempts are logged for security monitoring.
func CheckSourceIPAllowed(instanceID uint, allowedSourceIPs string, targetHost string, targetPort int) error {
	restriction, err := ParseIPRestrictions(allowedSourceIPs)
	if err != nil {
		return fmt.Errorf("parse IP restrictions for instance %d: %w", instanceID, err)
	}

	// No restrictions configured — allow all.
	if restriction == nil {
		return nil
	}

	// Determine our outbound IP for this target.
	outboundIP, err := GetOutboundIP(targetHost, targetPort)
	if err != nil {
		log.Printf("SSH IP restriction: instance %d — failed to determine outbound IP for %s:%d: %v",
			instanceID, targetHost, targetPort, err)
		return &ErrIPRestricted{
			InstanceID: instanceID,
			SourceIP:   "unknown",
			Reason:     fmt.Sprintf("failed to determine outbound IP: %v", err),
		}
	}

	if !restriction.IsAllowed(outboundIP) {
		log.Printf("SSH IP restriction: BLOCKED connection to instance %d — source IP %s not in allowed list [%s]",
			instanceID, outboundIP.String(), restriction.Raw)
		return &ErrIPRestricted{
			InstanceID: instanceID,
			SourceIP:   outboundIP.String(),
			Reason:     fmt.Sprintf("not in allowed list [%s]", restriction.Raw),
		}
	}

	log.Printf("SSH IP restriction: instance %d — source IP %s allowed", instanceID, outboundIP.String())
	return nil
}
