//go:build !linux

package utils

// Fork addition (plan 51): no TCP user timeout outside Linux (the keepalives
// and the server-side timeouts still bound the connection).

import (
	"syscall"
	"time"
)

func tcpUserTimeout(time.Duration) func(network, address string, c syscall.RawConn) error {
	return nil
}
