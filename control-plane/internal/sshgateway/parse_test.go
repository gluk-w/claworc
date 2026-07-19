package sshgateway

import (
	"reflect"
	"testing"
)

func TestParseSSHUser(t *testing.T) {
	tests := []struct {
		in           string
		wantUser     string
		wantInstance string
	}{
		{"stan+my-agent", "stan", "my-agent"},
		{"stan", "stan", ""},
		{"stan+", "stan", ""},
		{"+my-agent", "", "my-agent"},
		{"stan+bot-my-agent", "stan", "bot-my-agent"},
		{"a+b+c", "a", "b+c"},
		{"", "", ""},
	}
	for _, tt := range tests {
		user, instance := ParseSSHUser(tt.in)
		if user != tt.wantUser || instance != tt.wantInstance {
			t.Errorf("ParseSSHUser(%q) = (%q, %q), want (%q, %q)",
				tt.in, user, instance, tt.wantUser, tt.wantInstance)
		}
	}
}

func TestResolveInstanceName(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"my-agent", []string{"my-agent", "bot-my-agent"}},
		{"bot-my-agent", []string{"bot-my-agent"}},
		{"My-Agent", []string{"my-agent", "bot-my-agent"}},
		{"  my-agent  ", []string{"my-agent", "bot-my-agent"}},
		{"", nil},
		{"   ", nil},
	}
	for _, tt := range tests {
		got := ResolveInstanceName(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ResolveInstanceName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
