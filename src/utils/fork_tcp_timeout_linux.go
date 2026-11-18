//go:build linux

package utils

// Fork addition (plan 51): a TCP user timeout for the monitoring store's
// PostgreSQL connections, so that a database host that vanishes without a
// reset fails the connection in seconds, not after the kernel's
// retransmissions (about 15 minutes).

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func tcpUserTimeout(d time.Duration) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var failed error
		err := c.Control(func(fd uintptr) {
			failed = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d/time.Millisecond))
		})
		if err != nil {
			return err
		}
		return failed
	}
}
