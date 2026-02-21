package sshmanager

import (
	"strings"
	"testing"
)

// --- ParseAllowedIPs tests ---

func TestParseAllowedIPs_Empty(t *testing.T) {
	networks, err := ParseAllowedIPs("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if networks != nil {
		t.Errorf("expected nil for empty input, got %v", networks)
	}
}

func TestParseAllowedIPs_SingleIP(t *testing.T) {
	networks, err := ParseAllowedIPs("10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(networks) != 1 {
		t.Fatalf("expected 1 network, got %d", len(networks))
	}
	if !networks[0].IP.Equal(networks[0].IP) {
		t.Errorf("unexpected IP: %v", networks[0].IP)
	}
}

func TestParseAllowedIPs_CIDR(t *testing.T) {
	networks, err := ParseAllowedIPs("192.168.1.0/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(networks) != 1 {
		t.Fatalf("expected 1 network, got %d", len(networks))
	}
	if networks[0].String() != "192.168.1.0/24" {
		t.Errorf("expected 192.168.1.0/24, got %s", networks[0].String())
	}
}

func TestParseAllowedIPs_Multiple(t *testing.T) {
	networks, err := ParseAllowedIPs("10.0.0.1, 192.168.1.0/24, 172.16.0.0/12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(networks) != 3 {
		t.Fatalf("expected 3 networks, got %d", len(networks))
	}
}

func TestParseAllowedIPs_IPv6(t *testing.T) {
	networks, err := ParseAllowedIPs("2001:db8::1, 2001:db8::/32")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(networks) != 2 {
		t.Fatalf("expected 2 networks, got %d", len(networks))
	}
}

func TestParseAllowedIPs_InvalidIP(t *testing.T) {
	_, err := ParseAllowedIPs("not-an-ip")
	if err == nil {
		t.Fatal("expected error for invalid IP")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("expected 'invalid IP address' in error, got: %v", err)
	}
}

func TestParseAllowedIPs_InvalidCIDR(t *testing.T) {
	_, err := ParseAllowedIPs("10.0.0.0/99")
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
	if !strings.Contains(err.Error(), "invalid CIDR") {
		t.Errorf("expected 'invalid CIDR' in error, got: %v", err)
	}
}

func TestParseAllowedIPs_WhitespaceHandling(t *testing.T) {
	networks, err := ParseAllowedIPs("  10.0.0.1 , , 192.168.1.0/24  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(networks) != 2 {
		t.Fatalf("expected 2 networks (empty entries skipped), got %d", len(networks))
	}
}

// --- CheckIPAllowed tests ---

func TestCheckIPAllowed_EmptyAllowList(t *testing.T) {
	if err := CheckIPAllowed("1.2.3.4", ""); err != nil {
		t.Errorf("empty allow list should allow all, got error: %v", err)
	}
}

func TestCheckIPAllowed_MatchingSingleIP(t *testing.T) {
	if err := CheckIPAllowed("10.0.0.1", "10.0.0.1"); err != nil {
		t.Errorf("matching IP should be allowed: %v", err)
	}
}

func TestCheckIPAllowed_NonMatchingSingleIP(t *testing.T) {
	err := CheckIPAllowed("10.0.0.2", "10.0.0.1")
	if err == nil {
		t.Fatal("non-matching IP should be blocked")
	}
	if !strings.Contains(err.Error(), "not in the allowed list") {
		t.Errorf("expected 'not in the allowed list' in error, got: %v", err)
	}
}

func TestCheckIPAllowed_MatchingCIDR(t *testing.T) {
	if err := CheckIPAllowed("192.168.1.50", "192.168.1.0/24"); err != nil {
		t.Errorf("IP in CIDR range should be allowed: %v", err)
	}
}

func TestCheckIPAllowed_NonMatchingCIDR(t *testing.T) {
	err := CheckIPAllowed("192.168.2.1", "192.168.1.0/24")
	if err == nil {
		t.Fatal("IP outside CIDR range should be blocked")
	}
}

func TestCheckIPAllowed_MultipleEntries(t *testing.T) {
	allowList := "10.0.0.1, 192.168.1.0/24, 172.16.0.0/12"

	// Should match the single IP
	if err := CheckIPAllowed("10.0.0.1", allowList); err != nil {
		t.Errorf("should match single IP: %v", err)
	}

	// Should match the /24 CIDR
	if err := CheckIPAllowed("192.168.1.100", allowList); err != nil {
		t.Errorf("should match CIDR: %v", err)
	}

	// Should match the /12 CIDR
	if err := CheckIPAllowed("172.20.5.1", allowList); err != nil {
		t.Errorf("should match wider CIDR: %v", err)
	}

	// Should NOT match
	if err := CheckIPAllowed("8.8.8.8", allowList); err == nil {
		t.Error("should not match any entry")
	}
}

func TestCheckIPAllowed_IPv6Match(t *testing.T) {
	if err := CheckIPAllowed("2001:db8::5", "2001:db8::/32"); err != nil {
		t.Errorf("IPv6 in CIDR should be allowed: %v", err)
	}
}

func TestCheckIPAllowed_IPv6NoMatch(t *testing.T) {
	err := CheckIPAllowed("2001:db9::1", "2001:db8::/32")
	if err == nil {
		t.Fatal("IPv6 outside CIDR should be blocked")
	}
}

func TestCheckIPAllowed_InvalidSourceIP(t *testing.T) {
	err := CheckIPAllowed("not-an-ip", "10.0.0.1")
	if err == nil {
		t.Fatal("invalid source IP should return error")
	}
	if !strings.Contains(err.Error(), "could not parse source IP") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestCheckIPAllowed_InvalidAllowList(t *testing.T) {
	err := CheckIPAllowed("10.0.0.1", "garbage")
	if err == nil {
		t.Fatal("invalid allow list should return error")
	}
	if !strings.Contains(err.Error(), "invalid allow list") {
		t.Errorf("expected 'invalid allow list' in error, got: %v", err)
	}
}

// --- NormalizeAllowList tests ---

func TestNormalizeAllowList_Empty(t *testing.T) {
	result, err := NormalizeAllowList("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestNormalizeAllowList_SingleIP(t *testing.T) {
	result, err := NormalizeAllowList("10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1', got %q", result)
	}
}

func TestNormalizeAllowList_CIDRNormalization(t *testing.T) {
	// 10.0.0.5/24 should normalize to 10.0.0.0/24
	result, err := NormalizeAllowList("10.0.0.5/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "10.0.0.0/24" {
		t.Errorf("expected '10.0.0.0/24', got %q", result)
	}
}

func TestNormalizeAllowList_MultipleWithWhitespace(t *testing.T) {
	result, err := NormalizeAllowList("  10.0.0.1 ,  192.168.1.0/24 , ,")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "10.0.0.1, 192.168.1.0/24" {
		t.Errorf("expected '10.0.0.1, 192.168.1.0/24', got %q", result)
	}
}

func TestNormalizeAllowList_Invalid(t *testing.T) {
	_, err := NormalizeAllowList("not-an-ip")
	if err == nil {
		t.Fatal("expected error for invalid entry")
	}
}

// --- EventType constant ---

func TestEventIPRestrictedConstant(t *testing.T) {
	if EventIPRestricted != "ip_restricted" {
		t.Errorf("expected 'ip_restricted', got %q", EventIPRestricted)
	}
}
