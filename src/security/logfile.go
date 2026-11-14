package security

import (
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The Fail2ban file: one line per incident whose address is one the
// firewall can act on.
//
//	2026-11-13T10:22:01+0200 cosmos-security kind=auth.login.failed ip=203.0.113.7 sev=medium n=1
//
// An address on a line is always the peer of a connection to this server.
// Three kinds of incidents have no line:
//
//   - one whose address a proxy reported (Cloudflare for a hostname served
//     through it, a trusted reverse proxy): the firewall cannot cut that
//     visitor off, since the packets are the proxy's, and whoever reaches
//     the server from the proxy's addresses may have chosen the address. A
//     ban would only hit that address's own direct connections, which is how
//     a server is made to ban an innocent. SmartShield goes on counting and
//     blocking such a visitor inside Cosmos;
//   - one whose address is Cloudflare's own while a hostname is served
//     through Cloudflare: every visitor of such a hostname arrives from
//     those addresses;
//   - one from this machine or the local link (loopback, link-local).
//
// Everything on a line is produced here: the time, the kind (from the
// catalogue), the address (the parser's canonical form), the severity and a
// number. No text of the request is written, so there is nothing a client
// could use to forge a line or an address. The format is fixed: filters
// depend on it.
const (
	// LogFileName is the name of the file, in the configuration folder.
	LogFileName = "cosmos.security.log"
	// logTag follows the time on every line.
	logTag = "cosmos-security"
	// logTimeFormat is ISO 8601 with a numeric zone, which Fail2ban's default
	// date detection reads.
	logTimeFormat = "2006-01-02T15:04:05-0700"
	// the file is renamed to <name>.1 when it reaches this size, and a new
	// one is started: at most twice this on disk
	logMaxBytes = 5 << 20
)

// loggable reports whether an event belongs in the Fail2ban file: an
// incident from an address that can be acted on. Loopback, unspecified,
// link-local and multicast addresses never are, nor an address a proxy
// reported, nor one of Cloudflare's while a hostname is served through it.
func loggable(event Event) bool {
	if event.Class != ClassIncident || event.IP == "" || event.Forwarded {
		return false
	}
	if utils.CloudflareNeverBan(event.IP) {
		return false
	}
	addr, err := netip.ParseAddr(event.IP)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsValid() && !addr.IsLoopback() && !addr.IsUnspecified() &&
		!addr.IsLinkLocalUnicast() && !addr.IsLinkLocalMulticast() && !addr.IsMulticast() &&
		!addr.IsInterfaceLocalMulticast()
}

// logLine formats the line of an event. The event has been through clean:
// its kind is one of the catalogue and its address is canonical.
func logLine(event Event) string {
	var b strings.Builder
	b.Grow(128)
	b.WriteString(event.Time.Format(logTimeFormat))
	b.WriteByte(' ')
	b.WriteString(logTag)
	b.WriteString(" kind=")
	b.WriteString(event.Kind)
	b.WriteString(" ip=")
	b.WriteString(event.IP)
	b.WriteString(" sev=")
	b.WriteString(event.Severity)
	b.WriteString(" n=")
	b.WriteString(strconv.Itoa(event.Count))
	b.WriteByte('\n')
	return b.String()
}

// logFile appends to the Fail2ban file and rotates it by size: the full file
// is renamed to <name>.1 (replacing the one before) and a new file is
// created, which is the rotation logrotate does by default and the one
// Fail2ban's polling and inotify backends follow. Mode 0600.
type logFile struct {
	mu       sync.Mutex
	path     func() string
	maxBytes int64
	file     *os.File
	opened   string
	size     int64
}

func newLogFile(path func() string, maxBytes int64) *logFile {
	return &logFile{path: path, maxBytes: maxBytes}
}

func (l *logFile) open(path string) error {
	l.close()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	// an existing file keeps its mode on open: make sure of it
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	l.file, l.opened, l.size = file, path, info.Size()
	return nil
}

func (l *logFile) close() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

// current makes sure the open file is the one at path: the folder may have
// changed, or someone may have moved or deleted the file.
func (l *logFile) current(path string) error {
	if l.file != nil && l.opened == path {
		onDisk, errDisk := os.Stat(path)
		open, errOpen := l.file.Stat()
		if errDisk == nil && errOpen == nil && os.SameFile(onDisk, open) {
			return nil
		}
	}
	return l.open(path)
}

// write appends whole lines.
func (l *logFile) write(lines string) error {
	if lines == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	path := l.path()
	if err := l.current(path); err != nil {
		return err
	}
	if l.size > 0 && l.size+int64(len(lines)) > l.maxBytes {
		l.close()
		if err := os.Rename(path, path+".1"); err != nil {
			return err
		}
		if err := l.open(path); err != nil {
			return err
		}
	}
	n, err := l.file.WriteString(lines)
	l.size += int64(n)
	return err
}

// writeEvents writes the lines of the events that belong in the file.
func (l *logFile) writeEvents(events []Event) error {
	var b strings.Builder
	for _, event := range events {
		if loggable(event) {
			b.WriteString(logLine(event))
		}
	}
	return l.write(b.String())
}
