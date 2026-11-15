package utils

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOnlyTheRightTokenFromCloudflareArrives: wrong, missing, malformed,
// for another name, from outside Cloudflare's ranges, expired, reused.
func TestOnlyTheRightTokenFromCloudflareArrives(t *testing.T) {
	saved := watchTokens.now
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	watchTokens.now = func() time.Time { return now }
	defer func() { watchTokens.now = saved; watchTokens.list = nil }()

	note := func(host, peer, header string) {
		req := httptest.NewRequest("GET", "https://"+host+"/_health", nil)
		req.RemoteAddr = peer
		if header != "" {
			req.Header.Set(ReachWatchHeader, header)
		}
		req = req.WithContext(context.WithValue(req.Context(), "ClientID", "203.0.113.7"))
		NoteWatchProbe(req)
	}
	cloudflare := "173.245.48.9:40000"

	token := NewWatchToken("example.org")
	if len(token) != 32 {
		t.Fatalf("token %q", token)
	}
	wrong := strings.Repeat("0", 32)
	note("example.org:2053", cloudflare, wrong)
	note("example.org:2053", cloudflare, "")
	note("example.org:2053", cloudflare, token[:31])
	note("example.org:2053", cloudflare, token+"00")
	note("example.org:2053", cloudflare, strings.ToUpper(token)[:30]+"zz")
	note("other.example.org:2053", cloudflare, token)
	note("example.org:2053", "198.51.100.20:40000", token)
	note("example.org:2053", "192.168.1.20:40000", token)
	if arrived, _ := WatchTokenArrived(token); arrived {
		t.Fatal("a wrong request made the token arrive")
	}

	token = NewWatchToken("example.org")
	note("example.org:2053", cloudflare, token)
	arrived, visitor := WatchTokenArrived(token)
	if !arrived || visitor != "203.0.113.7" {
		t.Fatalf("the right request: %v %q", arrived, visitor)
	}
	if again, _ := WatchTokenArrived(token); again {
		t.Fatal("a token arrives once")
	}

	token = NewWatchToken("example.org")
	now = now.Add(61 * time.Second)
	note("example.org:2053", cloudflare, token)
	if arrived, _ := WatchTokenArrived(token); arrived {
		t.Fatal("an expired token arrived")
	}

	// a stranger cannot fill the list: tokens are made by the watch only,
	// and there are never more than a few
	watchTokens.list = nil
	for i := 0; i < watchTokensMax; i++ {
		NewWatchToken("example.org")
	}
	if NewWatchToken("example.org") != "" {
		t.Fatal("the list is not bounded")
	}
	watchTokens.list = nil

	// two headers are not one
	token = NewWatchToken("example.org")
	req := httptest.NewRequest("GET", "https://example.org/_health", nil)
	req.RemoteAddr = cloudflare
	req.Header.Add(ReachWatchHeader, token)
	req.Header.Add(ReachWatchHeader, token)
	NoteWatchProbe(req)
	if arrived, _ := WatchTokenArrived(token); arrived {
		t.Fatal("two headers made it arrive")
	}
}

func TestTheIntervalStaysWithinItsBounds(t *testing.T) {
	for _, c := range []struct{ minutes, want int }{{0, 5}, {-3, 2}, {1, 2}, {7, 7}, {61, 60}} {
		if got := ReachWatchInterval(ForkReachWatchConfig{Minutes: c.minutes}); got != time.Duration(c.want)*time.Minute {
			t.Errorf("%d: %v", c.minutes, got)
		}
	}
}
