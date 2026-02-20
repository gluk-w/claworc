//go:build linux && cgo

package neko

import (
	neko "github.com/m1k1o/neko/server"
)

// Re-export the embedded server types from the vendored Neko module.
type (
	EmbeddedServer = neko.EmbeddedServer
	EmbedOptions   = neko.EmbedOptions
)

// NewEmbeddedServer creates a new Neko embedded server with the given options.
var NewEmbeddedServer = neko.NewEmbeddedServer
