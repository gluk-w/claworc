package services

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Frame type markers for the terminal framing protocol.
const (
	frameBinary  byte = 0x01 // raw PTY data
	frameControl byte = 0x02 // JSON control message (length-prefixed)
)

// termInitHeader is the JSON header sent at the start of a terminal stream.
type termInitHeader struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// termControlMsg is a JSON control frame (e.g. resize).
type termControlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// HandleTerminalStream handles an interactive PTY session over a yamux stream.
// Protocol:
//  1. Read a JSON init header: {"cols":80,"rows":24}
//  2. Allocate a PTY running "su - abc"
//  3. Relay data using the framing protocol:
//     - [0x01][payload...]         = binary PTY data
//     - [0x02][varint len][json...] = control frame (e.g. resize)
func HandleTerminalStream(conn net.Conn) {
	defer conn.Close()

	// Read the JSON init header.
	dec := json.NewDecoder(conn)
	var init termInitHeader
	if err := dec.Decode(&init); err != nil {
		log.Printf("terminal: failed to read init header: %v", err)
		return
	}
	if init.Cols == 0 {
		init.Cols = 80
	}
	if init.Rows == 0 {
		init.Rows = 24
	}

	// Start the shell with a PTY.
	cmd := exec.Command("su", "-", "abc")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("terminal: failed to start pty: %v", err)
		return
	}
	defer ptmx.Close()

	// Set initial size.
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: init.Cols, Rows: init.Rows}); err != nil {
		log.Printf("terminal: failed to set initial size: %v", err)
	}

	var wg sync.WaitGroup

	// PTY stdout -> stream: wrap each chunk in a binary frame [0x01][data].
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				frame := make([]byte, 1+n)
				frame[0] = frameBinary
				copy(frame[1:], buf[:n])
				if _, werr := conn.Write(frame); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Stream -> PTY stdin: parse framing protocol.
	// The JSON decoder may have buffered bytes beyond the init header,
	// so we must read from a combined reader: the decoder's buffer first,
	// then the raw connection.
	buffered := dec.Buffered()
	reader := io.MultiReader(buffered, conn)
	func() {
		for {
			// Read the frame type byte.
			var typeBuf [1]byte
			if _, err := io.ReadFull(reader, typeBuf[:]); err != nil {
				return
			}

			switch typeBuf[0] {
			case frameBinary:
				// Read raw PTY data until the next frame marker.
				// Since binary frames are [0x01][payload...] with no length prefix,
				// we read available data in chunks and write to PTY.
				// The payload continues until the next frame type byte or EOF.
				// However, for efficiency we read a chunk at a time.
				data := make([]byte, 32*1024)
				n, err := reader.Read(data)
				if n > 0 {
					ptmx.Write(data[:n])
				}
				if err != nil {
					return
				}

			case frameControl:
				// Control frame: [0x02][varint length][json...]
				length, err := binary.ReadUvarint(&byteReader{r: reader})
				if err != nil {
					log.Printf("terminal: failed to read control frame length: %v", err)
					return
				}
				if length > 1024*1024 {
					log.Printf("terminal: control frame too large: %d", length)
					return
				}
				jsonBuf := make([]byte, length)
				if _, err := io.ReadFull(reader, jsonBuf); err != nil {
					log.Printf("terminal: failed to read control frame: %v", err)
					return
				}
				var msg termControlMsg
				if err := json.Unmarshal(jsonBuf, &msg); err != nil {
					log.Printf("terminal: failed to parse control frame: %v", err)
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
				}

			default:
				log.Printf("terminal: unknown frame type 0x%02x", typeBuf[0])
				return
			}
		}
	}()

	// Wait for the PTY reader goroutine to finish.
	wg.Wait()
	cmd.Wait()
}

// byteReader adapts an io.Reader to io.ByteReader for binary.ReadUvarint.
type byteReader struct {
	r io.Reader
}

func (br *byteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(br.r, b[:])
	return b[0], err
}
