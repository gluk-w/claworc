package logging

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gluk-w/claworc/control-plane/internal/config"
)

var (
	logFile *os.File
	mu      sync.Mutex
)

// Init sets up dual logging to stdout and a log file.
// Must be called after config.Load().
func Init() {
	path := config.Cfg.LogPath
	if path == "" {
		path = "/app/data/claworc.log"
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf("WARNING: cannot create log directory: %v", err)
		return
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("WARNING: cannot open log file %s: %v", path, err)
		return
	}

	logFile = f
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)
	log.Printf("Logging to file: %s", path)
}

// ReadTail returns the last n lines from the log file.
func ReadTail(n int) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	path := config.Cfg.LogPath
	if path == "" {
		path = "/app/data/claworc.log"
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	// Increase buffer for potentially long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan log file: %w", err)
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return strings.Join(lines, "\n"), nil
}

// Clear truncates the log file.
func Clear() error {
	mu.Lock()
	defer mu.Unlock()

	path := config.Cfg.LogPath
	if path == "" {
		path = "/app/data/claworc.log"
	}

	// Truncate the active log file
	if logFile != nil {
		if err := logFile.Truncate(0); err != nil {
			return fmt.Errorf("truncate log file: %w", err)
		}
		if _, err := logFile.Seek(0, 0); err != nil {
			return fmt.Errorf("seek log file: %w", err)
		}
		return nil
	}

	// Fallback: truncate by path
	return os.Truncate(path, 0)
}
