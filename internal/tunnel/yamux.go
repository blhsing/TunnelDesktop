package tunnel

import (
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

func YamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.MaxStreamWindowSize = 4 * 1024 * 1024
	cfg.LogOutput = io.Discard
	return cfg
}
