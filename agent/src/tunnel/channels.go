package tunnel

// Channel names for yamux stream multiplexing. Each yamux stream begins
// with a one-line header declaring the channel name followed by a newline
// (e.g. "neko\n"). The tunnel listener reads this header and routes the
// stream to the registered ChannelHandler.
const (
	ChannelGateway  = "gateway"
	ChannelNeko     = "neko"
	ChannelTerminal = "terminal"
	ChannelFiles    = "files"
	ChannelLogs     = "logs"
	ChannelPing     = "ping"
)
