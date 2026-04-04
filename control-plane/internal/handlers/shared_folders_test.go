package handlers

import (
	"sort"
	"testing"
)

func TestIsValidMountPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"/data/shared", true},
		{"/mnt/project", true},
		{"/home/claworc", false},           // reserved
		{"/home/claworc/data", false},       // reserved prefix
		{"/home/linuxbrew", false},          // reserved
		{"/home/linuxbrew/.linuxbrew", false},// reserved prefix
		{"/dev/shm", false},                 // reserved
		{"relative/path", false},            // not absolute
		{"", false},                         // empty
	}
	for _, tt := range tests {
		got := isValidMountPath(tt.path)
		if got != tt.want {
			t.Errorf("isValidMountPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMergeUintSets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a    []uint
		b    []uint
		want []uint
	}{
		{[]uint{1, 2}, []uint{3}, []uint{1, 2, 3}},
		{[]uint{1, 2}, []uint{2, 3}, []uint{1, 2, 3}},
		{nil, []uint{1}, []uint{1}},
		{nil, nil, []uint{}},
		{[]uint{}, []uint{}, []uint{}},
	}
	for _, tt := range tests {
		got := mergeUintSets(tt.a, tt.b)
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		sort.Slice(tt.want, func(i, j int) bool { return tt.want[i] < tt.want[j] })
		if len(got) != len(tt.want) {
			t.Errorf("mergeUintSets(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("mergeUintSets(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
				break
			}
		}
	}
}
