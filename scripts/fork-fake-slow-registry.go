//go:build ignore

// Fork addition: a slow line in front of a test registry, for the pull
// progress checks (src/docker/fork_pull_progress_live_test.go and
// scripts/fork-ui-check-pull-progress.mjs). It passes TCP connections on to
// the registry and lets the registry's answer through at <bytes per second>,
// so that a pull takes long enough to be seen. With <cut after bytes> above
// zero, every connection is closed after that many bytes of answer (a line
// that breaks: Docker resumes and the pull still works), or with "corrupt"
// the byte there is changed instead (Docker's download fails its checksum and
// the pull fails in the middle of its stream, with Docker's error line).
//
//	go run scripts/fork-fake-slow-registry.go <listen address> <registry host:port> <bytes per second> [after bytes] [cut|corrupt]
//
// Used on Docker networks made with --internal only. It is not part of any
// build.
package main

import (
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 4 {
		log.Fatal("usage: fork-fake-slow-registry.go <listen> <registry host:port> <bytes per second> [cut after bytes]")
	}
	rate, err := strconv.Atoi(os.Args[3])
	if err != nil || rate <= 0 {
		log.Fatal("bytes per second: a number above zero")
	}
	cut := 0
	if len(os.Args) > 4 {
		cut, _ = strconv.Atoi(os.Args[4])
	}
	corrupt := len(os.Args) > 5 && os.Args[5] == "corrupt"
	ln, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("slow line on %s to %s at %d B/s, after %d bytes corrupt=%v", os.Args[1], os.Args[2], rate, cut, corrupt)
	for {
		in, err := ln.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer in.Close()
			out, err := net.DialTimeout("tcp", os.Args[2], 5*time.Second)
			if err != nil {
				return
			}
			defer out.Close()
			go io.Copy(out, in)
			chunk := rate / 10
			if chunk < 1 {
				chunk = 1
			}
			buf := make([]byte, chunk)
			sent := 0
			for {
				n, err := out.Read(buf)
				if n > 0 {
					if cut > 0 && sent <= cut && sent+n > cut {
						if !corrupt {
							in.Write(buf[:cut-sent])
							return
						}
						buf[cut-sent] ^= 0xff
					}
					if _, werr := in.Write(buf[:n]); werr != nil {
						return
					}
					sent += n
					time.Sleep(time.Duration(n) * time.Second / time.Duration(rate))
				}
				if err != nil {
					return
				}
			}
		}()
	}
}
