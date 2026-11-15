package forkrelease

// Fork addition: does Docker answer the launcher? A start of a version on
// trial that ended because it could not reach Docker is not counted when
// Docker does not answer the launcher either (see trial.go).

import (
	"bufio"
	"net"
	"os"
	"strings"
	"time"
)

// dockerAddress is where the server's Docker client connects: DOCKER_HOST
// (unix:// or tcp://), else the default socket. ok is false for a form this
// code does not know.
func dockerAddress(host string) (network string, address string, ok bool) {
	switch {
	case host == "":
		return "unix", "/var/run/docker.sock", true
	case strings.HasPrefix(host, "unix://"):
		return "unix", strings.TrimPrefix(host, "unix://"), true
	case strings.HasPrefix(host, "tcp://"):
		address = strings.TrimPrefix(host, "tcp://")
		if i := strings.IndexByte(address, '/'); i >= 0 {
			address = address[:i]
		}
		return "tcp", address, address != ""
	}
	return "", "", false
}

// dockerAnswers: something answers HTTP at Docker's address within 2
// seconds. Any answer counts, a 400 of a Docker that speaks TLS to this
// plain request included: only no connection or no answer is "Docker
// cannot be reached". That errs towards counting the start. A form of
// DOCKER_HOST this code does not know counts as answering too.
var dockerAnswers = func() bool {
	network, address, ok := dockerAddress(os.Getenv("DOCKER_HOST"))
	if !ok {
		return true
	}
	conn, err := net.DialTimeout(network, address, 2*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("GET /_ping HTTP/1.0\r\nHost: docker\r\n\r\n")); err != nil {
		return false
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	return err == nil && strings.HasPrefix(line, "HTTP/")
}
