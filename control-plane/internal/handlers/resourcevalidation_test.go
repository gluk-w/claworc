package handlers

import "testing"

func TestValidateResourceQuantities(t *testing.T) {
	cases := []struct {
		name    string
		q       ResourceQuantities
		wantErr bool
	}{
		{"all empty", ResourceQuantities{}, false},
		{"valid full", ResourceQuantities{
			CPURequest: "500m", CPULimit: "2000m",
			MemoryRequest: "1Gi", MemoryLimit: "4Gi",
			StorageHome: "10Gi", StorageHomebrew: "10Gi",
		}, false},
		{"cpu decimal valid", ResourceQuantities{CPURequest: "0.5", CPULimit: "2"}, false},
		{"cpu junk", ResourceQuantities{CPURequest: "half"}, true},
		{"memory wrong unit", ResourceQuantities{MemoryRequest: "500Mb"}, true},
		{"memory bare int", ResourceQuantities{MemoryRequest: "500"}, true},
		{"memory Ki ok", ResourceQuantities{MemoryRequest: "1024Ki", MemoryLimit: "1Mi"}, false},
		{"storage wrong unit", ResourceQuantities{StorageHome: "10G"}, true},
		{"storage homebrew junk", ResourceQuantities{StorageHomebrew: "10gb"}, true},
		{"cpu req gt limit", ResourceQuantities{CPURequest: "3000m", CPULimit: "2000m"}, true},
		{"mem req gt limit", ResourceQuantities{MemoryRequest: "8Gi", MemoryLimit: "4Gi"}, true},
		{"req present limit empty (skip pair)", ResourceQuantities{CPURequest: "500m"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateResourceQuantities(tc.q)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
