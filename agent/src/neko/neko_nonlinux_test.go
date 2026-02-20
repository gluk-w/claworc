//go:build !linux

package neko

import (
	"testing"

	"github.com/gluk-w/claworc/agent/config"
)

func TestNew_NonLinuxReturnsError(t *testing.T) {
	cfg := &config.Config{}
	srv, err := New(cfg)
	if err == nil {
		t.Fatal("New on non-Linux should return error")
	}
	if srv != nil {
		t.Error("New on non-Linux should return nil server")
	}
}
