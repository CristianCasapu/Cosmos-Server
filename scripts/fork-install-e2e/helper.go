//go:build ignore

// Fork addition: small tools for the install scripts' test bed
// (scripts/fork-install-e2e.sh). Not part of any build.
//
//	helper lock-dpkg <seconds>        hold apt's and dpkg's locks (fcntl, as
//	                                  apt itself and unattended-upgrades do)
//	helper lock-file <path> <seconds> hold one lock file (apt's lists lock)
//	helper bad-zip <in> <out> <kind>  copy a release zip and add one entry
//	                                  install.sh must refuse: dotdot (a ".."
//	                                  path), absolute (a "/" path), symlink
//	                                  (a link to /etc), duplicate (a name
//	                                  twice), setuid, worldwritable, device
//	                                  (a character device), ctrlchar (a
//	                                  control character in a name) or bomb
//	                                  (3.5 GiB of zeros, honestly declared)
package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func lock(path string) *os.File {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		panic(err)
	}
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLKW, &lk); err != nil {
		panic(err)
	}
	return f
}

func badZip(in, out, kind string) error {
	r, err := zip.OpenReader(in)
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	top := ""
	if len(r.File) > 0 {
		top = strings.SplitN(r.File[0].Name, "/", 2)[0]
	}
	// longlist-<kind>: the bad entry FIRST, then the release, then thousands
	// of plain entries: a listing far over a pipe's 64 KiB, so that a check
	// written as "listing | grep -q" would stop reading at the bad entry and
	// the listing's writer die of SIGPIPE (under pipefail: "no match").
	if rest, ok := strings.CutPrefix(kind, "longlist-"); ok {
		if err := addBad(w, top, rest); err != nil {
			return err
		}
		for _, e := range r.File {
			if err := w.Copy(e); err != nil {
				return err
			}
		}
		for i := 0; i < 4000; i++ {
			h := &zip.FileHeader{Name: fmt.Sprintf("%s/padding/%s-%05d", top, strings.Repeat("p", 60), i), Method: zip.Store}
			h.SetMode(0o644)
			if _, err := w.CreateHeader(h); err != nil {
				return err
			}
		}
		return w.Close()
	}
	for _, e := range r.File {
		if err := w.Copy(e); err != nil {
			return err
		}
	}
	if err := addBad(w, top, kind); err != nil {
		return err
	}
	return w.Close()
}

func addBad(w *zip.Writer, top, kind string) error {
	var header *zip.FileHeader
	body := "planted by the test bed\n"
	switch kind {
	case "dotdot":
		header = &zip.FileHeader{Name: top + "/../../../etc/cosmos-e2e-planted", Method: zip.Deflate}
	case "absolute":
		header = &zip.FileHeader{Name: "/etc/cosmos-e2e-planted", Method: zip.Deflate}
	case "symlink":
		header = &zip.FileHeader{Name: top + "/planted-link", Method: zip.Store}
		header.SetMode(os.ModeSymlink | 0o777)
		body = "/etc"
	case "duplicate":
		header = &zip.FileHeader{Name: top + "/start.sh", Method: zip.Deflate}
		header.SetMode(0o755)
		body = "#!/bin/sh\necho planted\n"
	case "setuid":
		header = &zip.FileHeader{Name: top + "/planted-setuid", Method: zip.Deflate}
		header.SetMode(0o4755)
	case "worldwritable":
		header = &zip.FileHeader{Name: top + "/planted-writable", Method: zip.Deflate}
		header.SetMode(0o666)
	case "device":
		header = &zip.FileHeader{Name: top + "/planted-device", Method: zip.Store}
		header.SetMode(os.ModeDevice | os.ModeCharDevice | 0o600)
	case "ctrlchar":
		header = &zip.FileHeader{Name: top + "/planted\x07name", Method: zip.Deflate}
	case "bomb":
		header = &zip.FileHeader{Name: top + "/planted-bomb", Method: zip.Deflate}
		header.SetMode(0o644)
		ew, err := w.CreateHeader(header)
		if err != nil {
			return err
		}
		zeros := make([]byte, 1<<20)
		for i := 0; i < 3584; i++ {
			if _, err := ew.Write(zeros); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown kind %q", kind)
	}
	if header.Mode() == 0 {
		header.SetMode(0o644)
	}
	ew, err := w.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.WriteString(ew, body)
	return err
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "lock-dpkg" {
		seconds, _ := strconv.Atoi(os.Args[2])
		a := lock("/var/lib/dpkg/lock-frontend")
		b := lock("/var/lib/dpkg/lock")
		fmt.Println("locked")
		time.Sleep(time.Duration(seconds) * time.Second)
		a.Close()
		b.Close()
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "lock-file" {
		seconds, _ := strconv.Atoi(os.Args[3])
		f := lock(os.Args[2])
		fmt.Println("locked")
		time.Sleep(time.Duration(seconds) * time.Second)
		f.Close()
		return
	}
	if len(os.Args) == 5 && os.Args[1] == "bad-zip" {
		if err := badZip(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "usage: helper lock-dpkg <seconds> | helper bad-zip <in> <out> dotdot|absolute|symlink")
	os.Exit(2)
}
