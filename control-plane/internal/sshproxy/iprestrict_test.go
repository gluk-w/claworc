package sshproxy

import (
	"net"
	"testing"
)

// --- ParseIPRestrictions tests ---

func TestParseIPRestrictions_EmptyString(t *testing.T) {
	r, err := ParseIPRestrictions("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil restriction for empty string")
	}
}

func TestParseIPRestrictions_WhitespaceOnly(t *testing.T) {
	r, err := ParseIPRestrictions("   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil restriction for whitespace-only string")
	}
}

func TestParseIPRestrictions_SingleIPv4(t *testing.T) {
	r, err := ParseIPRestrictions("10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil restriction")
	}
	if len(r.IPs) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(r.IPs))
	}
	if !r.IPs[0].Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("expected 10.0.0.1, got %s", r.IPs[0])
	}
	if len(r.CIDRs) != 0 {
		t.Errorf("expected 0 CIDRs, got %d", len(r.CIDRs))
	}
}

func TestParseIPRestrictions_SingleIPv6(t *testing.T) {
	r, err := ParseIPRestrictions("::1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil restriction")
	}
	if len(r.IPs) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(r.IPs))
	}
	if !r.IPs[0].Equal(net.ParseIP("::1")) {
		t.Errorf("expected ::1, got %s", r.IPs[0])
	}
}

func TestParseIPRestrictions_SingleCIDR(t *testing.T) {
	r, err := ParseIPRestrictions("10.0.0.0/8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil restriction")
	}
	if len(r.CIDRs) != 1 {
		t.Fatalf("expected 1 CIDR, got %d", len(r.CIDRs))
	}
	if r.CIDRs[0].String() != "10.0.0.0/8" {
		t.Errorf("expected 10.0.0.0/8, got %s", r.CIDRs[0])
	}
	if len(r.IPs) != 0 {
		t.Errorf("expected 0 IPs, got %d", len(r.IPs))
	}
}

func TestParseIPRestrictions_MixedIPsAndCIDRs(t *testing.T) {
	r, err := ParseIPRestrictions("10.0.0.1, 192.168.0.0/16, 172.16.0.1, fd00::/8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil restriction")
	}
	if len(r.IPs) != 2 {
		t.Fatalf("expected 2 IPs, got %d", len(r.IPs))
	}
	if len(r.CIDRs) != 2 {
		t.Fatalf("expected 2 CIDRs, got %d", len(r.CIDRs))
	}
}

func TestParseIPRestrictions_WithExtraWhitespace(t *testing.T) {
	r, err := ParseIPRestrictions("  10.0.0.1 ,  192.168.1.0/24 ,  172.16.0.1  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil restriction")
	}
	if len(r.IPs) != 2 {
		t.Fatalf("expected 2 IPs, got %d", len(r.IPs))
	}
	if len(r.CIDRs) != 1 {
		t.Fatalf("expected 1 CIDR, got %d", len(r.CIDRs))
	}
}

func TestParseIPRestrictions_EmptyEntries(t *testing.T) {
	// Trailing comma produces empty entry which should be skipped
	r, err := ParseIPRestrictions("10.0.0.1,")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil restriction")
	}
	if len(r.IPs) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(r.IPs))
	}
}

func TestParseIPRestrictions_AllEmptyEntries(t *testing.T) {
	r, err := ParseIPRestrictions(", , ,")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil restriction for all-empty entries")
	}
}

func TestParseIPRestrictions_InvalidIP(t *testing.T) {
	_, err := ParseIPRestrictions("not-an-ip")
	if err == nil {
		t.Fatal("expected error for invalid IP")
	}
}

func TestParseIPRestrictions_InvalidCIDR(t *testing.T) {
	_, err := ParseIPRestrictions("10.0.0.0/99")
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestParseIPRestrictions_InvalidMixedWithValid(t *testing.T) {
	_, err := ParseIPRestrictions("10.0.0.1, bad-entry, 192.168.0.0/16")
	if err == nil {
		t.Fatal("expected error when any entry is invalid")
	}
}

// --- IsAllowed tests ---

func TestIPRestriction_IsAllowed_NilRestriction(t *testing.T) {
	var r *IPRestriction
	if !r.IsAllowed(net.ParseIP("1.2.3.4")) {
		t.Error("nil restriction should allow all IPs")
	}
}

func TestIPRestriction_IsAllowed_ExactIPMatch(t *testing.T) {
	r, _ := ParseIPRestrictions("10.0.0.1, 192.168.1.100")
	if !r.IsAllowed(net.ParseIP("10.0.0.1")) {
		t.Error("10.0.0.1 should be allowed")
	}
	if !r.IsAllowed(net.ParseIP("192.168.1.100")) {
		t.Error("192.168.1.100 should be allowed")
	}
	if r.IsAllowed(net.ParseIP("10.0.0.2")) {
		t.Error("10.0.0.2 should not be allowed")
	}
}

func TestIPRestriction_IsAllowed_CIDRMatch(t *testing.T) {
	r, _ := ParseIPRestrictions("10.0.0.0/8")
	if !r.IsAllowed(net.ParseIP("10.0.0.1")) {
		t.Error("10.0.0.1 should be in 10.0.0.0/8")
	}
	if !r.IsAllowed(net.ParseIP("10.255.255.255")) {
		t.Error("10.255.255.255 should be in 10.0.0.0/8")
	}
	if r.IsAllowed(net.ParseIP("11.0.0.1")) {
		t.Error("11.0.0.1 should not be in 10.0.0.0/8")
	}
}

func TestIPRestriction_IsAllowed_IPv6CIDR(t *testing.T) {
	r, _ := ParseIPRestrictions("fd00::/8")
	if !r.IsAllowed(net.ParseIP("fd00::1")) {
		t.Error("fd00::1 should be in fd00::/8")
	}
	if r.IsAllowed(net.ParseIP("fe80::1")) {
		t.Error("fe80::1 should not be in fd00::/8")
	}
}

func TestIPRestriction_IsAllowed_MixedIPAndCIDR(t *testing.T) {
	r, _ := ParseIPRestrictions("192.168.1.100, 10.0.0.0/8")
	// Exact IP match
	if !r.IsAllowed(net.ParseIP("192.168.1.100")) {
		t.Error("192.168.1.100 exact match should be allowed")
	}
	// CIDR match
	if !r.IsAllowed(net.ParseIP("10.50.60.70")) {
		t.Error("10.50.60.70 in 10.0.0.0/8 should be allowed")
	}
	// Neither
	if r.IsAllowed(net.ParseIP("172.16.0.1")) {
		t.Error("172.16.0.1 should not be allowed")
	}
}

func TestIPRestriction_IsAllowed_MultipleCIDRs(t *testing.T) {
	r, _ := ParseIPRestrictions("10.0.0.0/8, 192.168.0.0/16, 172.16.0.0/12")
	tests := []struct {
		ip      string
		allowed bool
	}{
		{"10.0.0.1", true},
		{"10.255.0.1", true},
		{"192.168.1.1", true},
		{"192.168.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.32.0.1", false},
		{"8.8.8.8", false},
		{"192.167.0.1", false},
	}
	for _, tc := range tests {
		got := r.IsAllowed(net.ParseIP(tc.ip))
		if got != tc.allowed {
			t.Errorf("IsAllowed(%s) = %v, want %v", tc.ip, got, tc.allowed)
		}
	}
}

func TestIPRestriction_IsAllowed_Subnet24(t *testing.T) {
	r, _ := ParseIPRestrictions("192.168.1.0/24")
	if !r.IsAllowed(net.ParseIP("192.168.1.1")) {
		t.Error("192.168.1.1 should be in /24")
	}
	if !r.IsAllowed(net.ParseIP("192.168.1.254")) {
		t.Error("192.168.1.254 should be in /24")
	}
	if r.IsAllowed(net.ParseIP("192.168.2.1")) {
		t.Error("192.168.2.1 should not be in 192.168.1.0/24")
	}
}

func TestIPRestriction_IsAllowed_SingleHostCIDR(t *testing.T) {
	r, _ := ParseIPRestrictions("10.0.0.1/32")
	if !r.IsAllowed(net.ParseIP("10.0.0.1")) {
		t.Error("10.0.0.1 should match /32")
	}
	if r.IsAllowed(net.ParseIP("10.0.0.2")) {
		t.Error("10.0.0.2 should not match /32")
	}
}

// --- ErrIPRestricted tests ---

func TestErrIPRestricted_Error(t *testing.T) {
	err := &ErrIPRestricted{
		InstanceID: 42,
		SourceIP:   "10.0.0.1",
		Reason:     "not in allowed list [192.168.0.0/16]",
	}
	msg := err.Error()
	if msg == "" {
		t.Fatal("error message should not be empty")
	}
	// Check it includes the key info
	for _, want := range []string{"42", "10.0.0.1", "192.168.0.0/16"} {
		if !contains(msg, want) {
			t.Errorf("error message %q should contain %q", msg, want)
		}
	}
}

// --- CheckSourceIPAllowed tests ---

func TestCheckSourceIPAllowed_EmptyAllowList(t *testing.T) {
	// Empty string means no restrictions.
	err := CheckSourceIPAllowed(1, "", "127.0.0.1", 22)
	if err != nil {
		t.Fatalf("empty allow list should permit all: %v", err)
	}
}

func TestCheckSourceIPAllowed_InvalidAllowList(t *testing.T) {
	err := CheckSourceIPAllowed(1, "not-valid", "127.0.0.1", 22)
	if err == nil {
		t.Fatal("invalid allow list should return error")
	}
}

func TestCheckSourceIPAllowed_LoopbackAllowed(t *testing.T) {
	// When connecting to localhost, the outbound IP is typically 127.0.0.1.
	// Allow the loopback range.
	err := CheckSourceIPAllowed(1, "127.0.0.0/8", "127.0.0.1", 22)
	if err != nil {
		t.Fatalf("loopback should be allowed by 127.0.0.0/8: %v", err)
	}
}

func TestCheckSourceIPAllowed_LoopbackBlocked(t *testing.T) {
	// When connecting to localhost, outbound IP is 127.0.0.1.
	// Only allow 10.0.0.0/8 — should block.
	err := CheckSourceIPAllowed(1, "10.0.0.0/8", "127.0.0.1", 22)
	if err == nil {
		t.Fatal("loopback should be blocked when only 10.0.0.0/8 is allowed")
	}
	if _, ok := err.(*ErrIPRestricted); !ok {
		t.Errorf("expected *ErrIPRestricted, got %T", err)
	}
}

// --- GetOutboundIP tests ---

func TestGetOutboundIP_Localhost(t *testing.T) {
	ip, err := GetOutboundIP("127.0.0.1", 22)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}
	// When connecting to localhost, outbound IP should be loopback.
	if !ip.IsLoopback() {
		t.Errorf("expected loopback IP, got %s", ip)
	}
}

func TestGetOutboundIP_ReturnsNonNil(t *testing.T) {
	// This test verifies we get a valid IP for any reachable target.
	ip, err := GetOutboundIP("8.8.8.8", 53)
	if err != nil {
		t.Skipf("skipping: cannot determine outbound IP (no network?): %v", err)
	}
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}
}

// helper
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
