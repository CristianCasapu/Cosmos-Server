package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

type SmartResponseWriterWrapper struct {
	http.ResponseWriter
	ClientID           string // shield identity (user or IP)
	ClientIP           string
	Status             int
	Bytes              int64
	ThrottleNext       int
	TimeStarted        time.Time
	TimeEnded          time.Time
	RequestCost        int
	Method             string
	budget             *clientBudget
	policy             utils.SmartShieldPolicy
	isOver             bool
	hasBeenInterrupted bool
	isPrivileged       bool
	headerWritten      bool
	shieldID           string
}

func (w *SmartResponseWriterWrapper) IsOver() bool {
	return w.isOver
}

// WriteHeader prices the request once its outcome is known: non-GET costs 5
// requests, an error response 30 times that, so failed logins and scraping
// exhaust a budget far faster than browsing does.
func (w *SmartResponseWriterWrapper) WriteHeader(status int) {
	if w.headerWritten {
		return
	}
	w.headerWritten = true
	w.Status = status
	w.RequestCost = 1
	if w.Method != "GET" {
		w.RequestCost = 5
	}
	if w.Status >= 400 {
		w.RequestCost *= 30
	}
	if w.budget != nil && w.RequestCost > 1 {
		w.budget.addRequests(w.RequestCost-1, time.Now())
	}
	if !w.IsOver() {
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *SmartResponseWriterWrapper) Write(p []byte) (int, error) {
	if w.budget != nil && !w.isPrivileged {
		userConsumed := w.budget.consumed(w.ClientID, time.Now())
		if !isAllowedToRequest(w.shieldID, w.policy, userConsumed) {
			utils.Log(fmt.Sprintf("SmartShield: %s has been blocked due to abuse", w.ClientID))
			w.isOver = true
			w.TimeEnded = time.Now()
			w.hasBeenInterrupted = true
			if !w.headerWritten {
				w.headerWritten = true
				w.Status = http.StatusServiceUnavailable
				w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
			}
			if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
				flusher.Flush()
			}
			return 0, errors.New("Pending request cancelled due to SmartShield")
		}
	}

	// initial throttle
	if w.ThrottleNext > 0 {
		time.Sleep(time.Duration(w.ThrottleNext) * time.Millisecond)
		w.ThrottleNext = 0
	}

	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(p)

	if err != nil {
		w.isOver = true
		w.TimeEnded = time.Now()
		w.hasBeenInterrupted = true
	}

	w.Bytes += int64(n)
	if w.budget != nil && n > 0 {
		w.budget.addBytes(int64(n), time.Now())
	}

	return n, err
}

func (w *SmartResponseWriterWrapper) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *SmartResponseWriterWrapper) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}
