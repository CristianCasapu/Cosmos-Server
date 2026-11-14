package certs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// What is remembered about the orders of each certificate, so that a
// restart (every settings save is one) does not forget that the last order
// failed ten minutes ago. Kept next to the configuration, in a file of its
// own: it holds no secret and changes often.

const stateFileName = "acme-state.json"

// The bounds on orders, per certificate. Let's Encrypt allows 5
// certificates for the same set of names in 7 days, 50 per registered
// domain in 7 days and 5 failed validations per name and hour; these stay
// well below, whatever asks for the order. A changed set of names (an
// application was added) is a new certificate to the CA and only counts
// towards the totals.
const (
	maxOrdersPerDay     = 10
	maxOrdersPerWeek    = 30
	maxSameNamesPerWeek = 3
	firstBackoff        = time.Hour
	longestBackoff      = 24 * time.Hour
	longestRetryAfter   = 7 * 24 * time.Hour
	keepHistory         = 8 * 24 * time.Hour
	forgetAfter         = 120 * 24 * time.Hour
)

type ariState struct {
	Serial  string `json:"serial"`
	Start   int64  `json:"start,omitempty"`
	End     int64  `json:"end,omitempty"`
	Pick    int64  `json:"pick,omitempty"`
	Checked int64  `json:"checked,omitempty"`
	Again   int64  `json:"again,omitempty"` // not asked again before
}

// issuedCert is one certificate received: when, and for which set of names
// (a short digest of the sorted names).
type issuedCert struct {
	At    int64  `json:"at"`
	Names string `json:"names"`
}

// namesDigest identifies a set of names whatever their order.
func namesDigest(names []string) string {
	sorted := []string{}
	for _, name := range names {
		sorted = append(sorted, normalName(name))
	}
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:8])
}

type certState struct {
	// Attempts are the moments (Unix seconds) orders were started and
	// Issued the certificates received, of the last eight days.
	Attempts []int64      `json:"attempts,omitempty"`
	Issued   []issuedCert `json:"issued,omitempty"`
	// Failures counts the failed orders since the last success.
	Failures int `json:"failures,omitempty"`

	LastAt     int64  `json:"lastAt,omitempty"`
	LastOK     bool   `json:"lastOK,omitempty"`
	LastCode   string `json:"lastCode,omitempty"`
	LastDetail string `json:"lastDetail,omitempty"`
	LastCA     string `json:"lastCA,omitempty"`

	// WaitUntil: no automatic order before (back-off). A forced order may.
	WaitUntil int64 `json:"waitUntil,omitempty"`
	// HardUntil: the CA asked to wait until then. Nothing may order before.
	HardUntil int64 `json:"hardUntil,omitempty"`

	// IssuedSerial and IssuedDirectory say which certificate the last
	// success gave and where it came from.
	IssuedSerial    string `json:"issuedSerial,omitempty"`
	IssuedDirectory string `json:"issuedDirectory,omitempty"`

	ARI *ariState `json:"ari,omitempty"`

	// Noticed: when each notice about this certificate was last raised.
	Noticed map[string]int64 `json:"noticed,omitempty"`
}

type stateData struct {
	Certs map[string]*certState `json:"certs"`
	// Untrusted is the list of addresses the last "not trusted" notice
	// named, and when; FirstSeen is when each address was first found so.
	Untrusted   []string         `json:"untrusted,omitempty"`
	UntrustedAt int64            `json:"untrustedAt,omitempty"`
	FirstSeen   map[string]int64 `json:"firstSeen,omitempty"`
}

type stateStore struct {
	mu     sync.Mutex
	loaded bool
	data   stateData
	// forced: certificates an admin asked to renew now. Not kept across a
	// restart: the request is for now.
	forced map[string]bool
}

var store = &stateStore{}

func statePath() string { return filepath.Join(configFolder(), stateFileName) }

// load reads the file once. A file that cannot be read starts empty: what
// is lost is the memory of failures, never a certificate.
func (s *stateStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.data = stateData{Certs: map[string]*certState{}}
	if s.forced == nil {
		s.forced = map[string]bool{}
	}
	content, err := os.ReadFile(statePath())
	if err != nil {
		return
	}
	read := stateData{}
	if err := json.Unmarshal(content, &read); err != nil {
		utils.Warn("Certificates: " + stateFileName + " cannot be read and starts over")
		return
	}
	if read.Certs == nil {
		read.Certs = map[string]*certState{}
	}
	for key, cert := range read.Certs {
		if cert == nil {
			delete(read.Certs, key)
		}
	}
	s.data = read
}

func (s *stateStore) save() {
	content, err := json.MarshalIndent(s.data, "", " ")
	if err != nil {
		return
	}
	if err := writePrivate(statePath(), content); err != nil {
		utils.Warn("Certificates: " + stateFileName + " could not be written: " + clean(err.Error(), 200))
	}
}

// reset forgets everything in memory (tests, and a changed config folder).
func (s *stateStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	s.forced = map[string]bool{}
}

func (s *stateStore) cert(key string) *certState {
	s.load()
	cert, ok := s.data.Certs[key]
	if !ok {
		cert = &certState{}
		s.data.Certs[key] = cert
	}
	return cert
}

// get returns a copy of what is known about a certificate.
func (s *stateStore) get(key string) certState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if cert, ok := s.data.Certs[key]; ok {
		copied := *cert
		copied.Attempts = append([]int64{}, cert.Attempts...)
		copied.Issued = append([]issuedCert{}, cert.Issued...)
		if cert.ARI != nil {
			ari := *cert.ARI
			copied.ARI = &ari
		}
		return copied
	}
	return certState{}
}

func since(moments []int64, from time.Time) []int64 {
	kept := []int64{}
	for _, moment := range moments {
		if moment > from.Unix() {
			kept = append(kept, moment)
		}
	}
	return kept
}

// Why an order may not start.
const (
	waitRateLimited = "rateLimited" // the CA said when to come back
	waitDaily       = "daily"       // ten orders in 24 hours
	waitWeekly      = "weekly"      // thirty orders in 7 days
	waitSameNames   = "sameNames"   // three certificates for the same names in 7 days
	waitBackoff     = "backoff"     // the last order failed a short while ago
)

// freeAgain is when the oldest of the last max moments leaves the period.
func freeAgain(moments []int64, max int, period time.Duration) time.Time {
	sort.Slice(moments, func(i, j int) bool { return moments[i] < moments[j] })
	return time.Unix(moments[len(moments)-max], 0).Add(period)
}

// mayOrder says whether an order for the certificate may start now, and if
// not why and from when it may. force (an admin asked) skips the back-off
// after a failure; nothing skips the CA's own "come back later" or the
// bounds per day and week.
func (s *stateStore) mayOrder(key string, names []string, force bool, now time.Time) (bool, string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	cert, ok := s.data.Certs[key]
	if !ok {
		return true, "", time.Time{}
	}
	if cert.HardUntil > now.Unix() {
		return false, waitRateLimited, time.Unix(cert.HardUntil, 0)
	}
	if today := since(cert.Attempts, now.Add(-24*time.Hour)); len(today) >= maxOrdersPerDay {
		return false, waitDaily, freeAgain(today, maxOrdersPerDay, 24*time.Hour)
	}
	if week := since(cert.Attempts, now.Add(-7*24*time.Hour)); len(week) >= maxOrdersPerWeek {
		return false, waitWeekly, freeAgain(week, maxOrdersPerWeek, 7*24*time.Hour)
	}
	digest := namesDigest(names)
	same := []int64{}
	for _, issued := range cert.Issued {
		if issued.Names == digest && issued.At > now.Add(-7*24*time.Hour).Unix() {
			same = append(same, issued.At)
		}
	}
	if len(same) >= maxSameNamesPerWeek {
		return false, waitSameNames, freeAgain(same, maxSameNamesPerWeek, 7*24*time.Hour)
	}
	if !force && !s.forced[key] && cert.WaitUntil > now.Unix() {
		return false, waitBackoff, time.Unix(cert.WaitUntil, 0)
	}
	return true, "", time.Time{}
}

// started notes that an order is about to contact the CA. Written before
// it does: a process that dies during orders still counts them.
func (s *stateStore) started(key string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cert := s.cert(key)
	cert.Attempts = append(since(cert.Attempts, now.Add(-keepHistory)), now.Unix())
	s.prune(now)
	s.save()
}

// backoff is how long to wait after the n-th failure in a row.
func backoff(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	wait := firstBackoff
	for i := 1; i < failures && wait < longestBackoff; i++ {
		wait *= 2
	}
	if wait > longestBackoff {
		wait = longestBackoff
	}
	return wait
}

// finished notes how an order for these names ended. serial is the
// certificate received, failure why none was.
func (s *stateStore) finished(key string, now time.Time, ca CA, names []string, serial string, failure *Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cert := s.cert(key)
	delete(s.forced, key)
	cert.LastAt = now.Unix()
	cert.LastCA = ca.Name
	if failure == nil {
		cert.LastOK = true
		cert.LastCode = ""
		cert.LastDetail = ""
		cert.Failures = 0
		cert.WaitUntil = 0
		cert.HardUntil = 0
		kept := []issuedCert{}
		for _, issued := range cert.Issued {
			if issued.At > now.Add(-keepHistory).Unix() {
				kept = append(kept, issued)
			}
		}
		cert.Issued = append(kept, issuedCert{At: now.Unix(), Names: namesDigest(names)})
		cert.IssuedSerial = serial
		cert.IssuedDirectory = ca.Directory
		cert.ARI = nil
		cert.Noticed = nil
	} else {
		cert.LastOK = false
		cert.LastCode = failure.Code
		cert.LastDetail = failure.Detail
		cert.Failures++
		cert.WaitUntil = now.Add(backoff(cert.Failures)).Unix()
		if failure.Code == FailRateLimited {
			wait := failure.RetryAfter
			if wait <= 0 {
				wait = firstBackoff
			}
			if wait > longestRetryAfter {
				wait = longestRetryAfter
			}
			cert.HardUntil = now.Add(wait).Unix()
		}
	}
	s.save()
}

// prune drops certificates nothing was noted about for months.
func (s *stateStore) prune(now time.Time) {
	for key, cert := range s.data.Certs {
		if cert.LastAt != 0 && cert.LastAt < now.Add(-forgetAfter).Unix() && len(since(cert.Attempts, now.Add(-keepHistory))) == 0 {
			delete(s.data.Certs, key)
		}
	}
}

// force marks a certificate as asked for by an admin: due now, and the
// back-off does not apply to its next order.
func (s *stateStore) force(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.forced[key] = true
}

func (s *stateStore) unforce(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.forced, key)
}

func (s *stateStore) isForced(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forced[key]
}

// update changes what is known about a certificate and writes it.
func (s *stateStore) update(key string, change func(cert *certState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s.cert(key))
	s.save()
}

// updateAll changes the part that is not about one certificate.
func (s *stateStore) updateAll(change func(data *stateData)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	change(&s.data)
	s.save()
}

func (s *stateStore) snapshot() stateData {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	copied := stateData{Untrusted: append([]string{}, s.data.Untrusted...), UntrustedAt: s.data.UntrustedAt, FirstSeen: map[string]int64{}}
	for name, at := range s.data.FirstSeen {
		copied.FirstSeen[name] = at
	}
	return copied
}
