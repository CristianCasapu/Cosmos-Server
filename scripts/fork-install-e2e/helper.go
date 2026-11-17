//go:build ignore

// Fork addition: small tools for the install scripts' test bed
// (scripts/fork-install-e2e.sh). Not part of any build.
//
//	helper lock-dpkg <seconds>        hold apt's and dpkg's locks (fcntl, as
//	                                  apt itself and unattended-upgrades do)
//	helper bad-zip <in> <out> <kind>  copy a release zip and add one entry
//	                                  install.sh must refuse: dotdot (a ".."
//	                                  path), absolute (a "/" path) or symlink
//	                                  (a link to /etc)
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
	for _, e := range r.File {
		if top == "" {
			top = strings.SplitN(e.Name, "/", 2)[0]
		}
		if err := w.Copy(e); err != nil {
			return err
		}
	}
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
	if _, err := io.WriteString(ew, body); err != nil {
		return err
	}
	return w.Close()
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
