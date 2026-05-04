package handlers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	cpuRegex        = regexp.MustCompile(`^(\d+m|\d+(\.\d+)?)$`)
	memoryRegex     = regexp.MustCompile(`^\d+(Ki|Mi|Gi)$`)
	resolutionRegex = regexp.MustCompile(`^\d+x\d+$`)
)

func cpuToMillicores(s string) int64 {
	if strings.HasSuffix(s, "m") {
		n, _ := strconv.ParseInt(s[:len(s)-1], 10, 64)
		return n
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f * 1000)
}

func memoryToBytes(s string) int64 {
	unitMap := map[string]int64{"Ki": 1024, "Mi": 1024 * 1024, "Gi": 1024 * 1024 * 1024}
	for suffix, multiplier := range unitMap {
		if strings.HasSuffix(s, suffix) {
			n, _ := strconv.ParseInt(s[:len(s)-len(suffix)], 10, 64)
			return n * multiplier
		}
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// ResourceQuantities holds the six string values that get validated together.
// Empty strings are treated as "not provided" and skipped.
type ResourceQuantities struct {
	CPURequest      string
	CPULimit        string
	MemoryRequest   string
	MemoryLimit     string
	StorageHome     string
	StorageHomebrew string
}

// ValidateResourceQuantities checks each provided field against the expected
// k8s-quantity format and enforces request <= limit when both sides are
// present. It returns a user-facing error suitable for a 400 response.
func ValidateResourceQuantities(q ResourceQuantities) error {
	if q.CPURequest != "" && !cpuRegex.MatchString(q.CPURequest) {
		return fmt.Errorf("Invalid CPU request format (e.g., 500m or 0.5)")
	}
	if q.CPULimit != "" && !cpuRegex.MatchString(q.CPULimit) {
		return fmt.Errorf("Invalid CPU limit format (e.g., 2000m or 2)")
	}
	if q.MemoryRequest != "" && !memoryRegex.MatchString(q.MemoryRequest) {
		return fmt.Errorf("Invalid memory request format (e.g., 1Gi or 512Mi)")
	}
	if q.MemoryLimit != "" && !memoryRegex.MatchString(q.MemoryLimit) {
		return fmt.Errorf("Invalid memory limit format (e.g., 4Gi or 2048Mi)")
	}
	if q.StorageHome != "" && !memoryRegex.MatchString(q.StorageHome) {
		return fmt.Errorf("Invalid home storage format (e.g., 10Gi or 512Mi)")
	}
	if q.StorageHomebrew != "" && !memoryRegex.MatchString(q.StorageHomebrew) {
		return fmt.Errorf("Invalid homebrew storage format (e.g., 10Gi or 512Mi)")
	}

	if q.CPURequest != "" && q.CPULimit != "" &&
		cpuToMillicores(q.CPURequest) > cpuToMillicores(q.CPULimit) {
		return fmt.Errorf("CPU request cannot exceed CPU limit")
	}
	if q.MemoryRequest != "" && q.MemoryLimit != "" &&
		memoryToBytes(q.MemoryRequest) > memoryToBytes(q.MemoryLimit) {
		return fmt.Errorf("Memory request cannot exceed memory limit")
	}
	return nil
}
