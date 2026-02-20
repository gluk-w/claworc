package services

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
)

// logsHeader is the JSON header sent at the start of a logs stream.
type logsHeader struct {
	Tail   int  `json:"tail"`
	Follow bool `json:"follow"`
}

// HandleLogsStream streams OpenClaw logs over a yamux stream.
// Protocol:
//  1. Read a JSON header: {"tail":100,"follow":true}
//  2. Run `openclaw logs --plain --limit N` (optionally with --follow)
//  3. Stream stdout line-by-line as plain text (newline terminated)
func HandleLogsStream(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	var header logsHeader
	if err := dec.Decode(&header); err != nil {
		log.Printf("logs: failed to read header: %v", err)
		return
	}

	if header.Tail <= 0 {
		header.Tail = 100
	}

	args := []string{"logs", "--plain", "--limit", strconv.Itoa(header.Tail)}
	if header.Follow {
		args = append(args, "--follow")
	}

	cmd := exec.Command("openclaw", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("logs: failed to create stdout pipe: %v", err)
		fmt.Fprintf(conn, "error: failed to create stdout pipe: %v\n", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("logs: failed to start openclaw logs: %v", err)
		fmt.Fprintf(conn, "error: failed to start openclaw logs: %v\n", err)
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if _, err := fmt.Fprintf(conn, "%s\n", line); err != nil {
			break
		}
	}

	cmd.Wait()
}
