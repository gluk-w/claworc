package tunnel

// Channel names for yamux stream multiplexing. The control-plane writes a
// one-line header (e.g. "neko\n") at the start of each yamux stream so the
// agent-side router can dispatch it to the correct handler.
const (
	ChannelGateway  = "gateway"
	ChannelNeko     = "neko"
	ChannelTerminal = "terminal"
	ChannelFiles    = "files"
	ChannelLogs     = "logs"
)
