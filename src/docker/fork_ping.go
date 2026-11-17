package docker

// Fork addition (plan 48): one bounded question to Docker for the watch of
// src/watch, through the server's own client. When there is no client (it
// was lost), Connect makes it again, as the rest of the server does.

import (
	"context"
	"errors"
	"time"
)

// ForkPing asks Docker whether it answers, within timeout.
func ForkPing(timeout time.Duration) error {
	dockerConnectLock.Lock()
	c := DockerClient
	dockerConnectLock.Unlock()
	if c == nil {
		if err := Connect(); err != nil {
			return err
		}
		dockerConnectLock.Lock()
		c = DockerClient
		dockerConnectLock.Unlock()
		if c == nil {
			return errors.New("not connected to Docker")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := c.Ping(ctx)
	return err
}
