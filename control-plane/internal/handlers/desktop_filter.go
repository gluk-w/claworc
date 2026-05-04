package handlers

import (
	"encoding/binary"
)

// rfbClientFilter parses the client→server byte stream of an RFB session and
// selectively drops ClientSetDesktopSize (msg type 251) messages based on the
// session's primary status. The filter is byte-stream-oriented; partial
// messages are buffered until complete.
//
// We only need to parse messages well enough to find SetDesktopSize boundaries.
// For unknown message types we set bypass=true and pass everything through —
// this preserves correctness at the cost of disabling the filter for that
// session, which is acceptable: the worst case is that the X display flaps,
// the same behaviour we had before this change.
type rfbClientFilter struct {
	// Bytes of RFB handshake still expected from the client before the
	// message stream begins. With `SecurityTypes None` the client sends:
	//   ProtocolVersion (12) + chosen SecurityType (1) + ClientInit (1) = 14
	handshakeRemaining int
	msgBuffer          []byte
	session            *viewerSession
	bypass             bool
}

func newRFBClientFilter(s *viewerSession) *rfbClientFilter {
	return &rfbClientFilter{
		handshakeRemaining: 14,
		session:            s,
	}
}

// Process consumes incoming bytes from the client and returns the bytes that
// should be forwarded to the upstream noVNC server. SetDesktopSize messages
// from non-primary viewers are recorded (for later replay on promotion) but
// not forwarded.
func (f *rfbClientFilter) Process(in []byte) []byte {
	if f.bypass {
		return in
	}
	var out []byte
	if f.handshakeRemaining > 0 {
		n := f.handshakeRemaining
		if n > len(in) {
			n = len(in)
		}
		out = append(out, in[:n]...)
		f.handshakeRemaining -= n
		in = in[n:]
		if len(in) == 0 {
			return out
		}
	}
	f.msgBuffer = append(f.msgBuffer, in...)
	for len(f.msgBuffer) > 0 {
		msgLen, msgType, status := f.peekMessageLength(f.msgBuffer)
		switch status {
		case parseNeedMore:
			return out
		case parseUnknown:
			f.bypass = true
			out = append(out, f.msgBuffer...)
			f.msgBuffer = nil
			return out
		}
		if msgLen > len(f.msgBuffer) {
			return out
		}
		msg := f.msgBuffer[:msgLen]
		if msgType == rfbMsgSetDesktopSize {
			f.session.recordSetDesktopSize(msg)
			if f.session.isPrimary() {
				out = append(out, msg...)
			}
			// Non-primary: drop. The recorded bytes will be replayed if
			// this session is later promoted.
		} else {
			out = append(out, msg...)
		}
		f.msgBuffer = f.msgBuffer[msgLen:]
	}
	return out
}

const (
	rfbMsgSetPixelFormat           = 0
	rfbMsgSetEncodings             = 2
	rfbMsgFramebufferUpdateRequest = 3
	rfbMsgKeyEvent                 = 4
	rfbMsgPointerEvent             = 5
	rfbMsgClientCutText            = 6
	rfbMsgSetDesktopSize           = 251
)

type parseStatus int

const (
	parseOK parseStatus = iota
	parseNeedMore
	parseUnknown
)

// peekMessageLength returns the total byte length of the client message at
// the head of buf and its type. parseNeedMore means buf is too short to
// determine the length yet; parseUnknown means we don't know how to parse
// this message type and the caller should bypass the filter.
func (f *rfbClientFilter) peekMessageLength(buf []byte) (int, byte, parseStatus) {
	if len(buf) < 1 {
		return 0, 0, parseNeedMore
	}
	t := buf[0]
	switch t {
	case rfbMsgSetPixelFormat:
		return 20, t, parseOK
	case rfbMsgSetEncodings:
		if len(buf) < 4 {
			return 0, t, parseNeedMore
		}
		n := int(binary.BigEndian.Uint16(buf[2:4]))
		return 4 + 4*n, t, parseOK
	case rfbMsgFramebufferUpdateRequest:
		return 10, t, parseOK
	case rfbMsgKeyEvent:
		return 8, t, parseOK
	case rfbMsgPointerEvent:
		return 6, t, parseOK
	case rfbMsgClientCutText:
		if len(buf) < 8 {
			return 0, t, parseNeedMore
		}
		n := int(binary.BigEndian.Uint32(buf[4:8]))
		return 8 + n, t, parseOK
	case rfbMsgSetDesktopSize:
		if len(buf) < 4 {
			return 0, t, parseNeedMore
		}
		// Layout: type(1) pad(1) width(2) height(2) num-screens(1) pad(1)
		//   then num-screens × ScreenInfo(16). num-screens is at offset 6.
		if len(buf) < 8 {
			return 0, t, parseNeedMore
		}
		n := int(buf[6])
		return 8 + 16*n, t, parseOK
	default:
		return 0, t, parseUnknown
	}
}
