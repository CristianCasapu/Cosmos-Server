package user

// Fork addition: the session cookie is signed and checked with golang-jwt
// (moved from the unmaintained v3 to v4). These tests pin the behaviour
// RefreshUserToken relies on: a token SendUserToken issued is accepted, and
// expired, not-yet-valid, foreign-key, re-signed, "none" and HMAC-confused
// tokens are refused before any user lookup.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	jwt "github.com/golang-jwt/jwt/v4"
)


func forkTokenConfig(t *testing.T) (pub, priv string) {
	t.Helper()
	pub, priv = utils.GenerateEd25519Certificates()
	prev := utils.GetBaseMainConfig()
	c := utils.Config{}
	c.HTTPConfig.AuthPublicKey = pub
	c.HTTPConfig.AuthPrivateKey = priv
	utils.LoadBaseMainConfig(c)
	t.Cleanup(func() { utils.LoadBaseMainConfig(prev) })
	return pub, priv
}

func forkRefresh(t *testing.T, token string) error {
	t.Helper()
	req := httptest.NewRequest("GET", "http://localhost/cosmos/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "jwttoken", Value: token})
	_, _, _, err := RefreshUserToken(httptest.NewRecorder(), req)
	return err
}

func forkSign(t *testing.T, priv string, claims jwt.MapClaims) string {
	t.Helper()
	key, err := jwt.ParseEdPrivateKeyFromPEM([]byte(priv))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func forkClaims(iat, nbf, exp time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"nickname": "alice", "passwordCycle": 0, "mfaDone": true, "forDomain": "localhost",
		"permissions": []interface{}{}, "iat": iat.Unix(), "nbf": nbf.Unix(), "exp": exp.Unix(),
	}
}

func TestForkSessionTokenIssuedIsAccepted(t *testing.T) {
	forkTokenConfig(t)
	rec := httptest.NewRecorder()
	SendUserToken(rec, httptest.NewRequest("POST", "http://localhost/cosmos/api/login", nil),
		utils.User{Nickname: "alice", Role: utils.ADMIN}, true, false)
	var token string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "jwttoken" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("SendUserToken set no jwttoken cookie")
	}
	// The token passes the signature and claims checks; the test has no user
	// store, so the request stops at the user lookup that comes next.
	err := forkRefresh(t, token)
	if err == nil || err.Error() != "User not found" {
		t.Fatalf("a freshly issued token gave %v, want it to reach the user lookup", err)
	}
}

func TestForkSessionTokenRefusals(t *testing.T) {
	pub, priv := forkTokenConfig(t)
	now := time.Now()
	_, otherPriv := utils.GenerateEd25519Certificates()

	valid := forkSign(t, priv, forkClaims(now, now, now.Add(time.Hour)))
	parts := strings.Split(valid, ".")
	otherPayload := strings.Split(forkSign(t, priv, jwt.MapClaims{"nickname": "admin", "forDomain": "localhost"}), ".")[1]

	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, forkClaims(now, now, now.Add(time.Hour))).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	hmac, err := jwt.NewWithClaims(jwt.SigningMethodHS256, forkClaims(now, now, now.Add(time.Hour))).SignedString([]byte(pub))
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"expired":             forkSign(t, priv, forkClaims(now.Add(-3*time.Hour), now.Add(-3*time.Hour), now.Add(-time.Hour))),
		"not valid yet":       forkSign(t, priv, forkClaims(now, now.Add(time.Hour), now.Add(2*time.Hour))),
		"issued in future":    forkSign(t, priv, forkClaims(now.Add(time.Hour), now, now.Add(2*time.Hour))),
		"other key":           forkSign(t, otherPriv, forkClaims(now, now, now.Add(time.Hour))),
		"swapped payload":     parts[0] + "." + otherPayload + "." + parts[2],
		"alg none":            none,
		"hmac with pub key":   hmac,
		"signature truncated": parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-4],
	}
	for name, token := range cases {
		// Refused means: an error before the user lookup ("User not found"
		// would mean the token was taken as genuine).
		err := forkRefresh(t, token)
		if err == nil || err.Error() == "User not found" {
			t.Errorf("%s: got %v, want the token refused", name, err)
		}
	}
}
