package user

// Fork addition: a session that was logged out stays logged out.
//
// Logging out used to clear the cookie in the browser and nothing else: the
// same cookie, kept somewhere, went on working until it expired two weeks
// later. The token that is logged out is now remembered (as a hash, until
// its own expiry, in revoked-sessions.json next to the configuration) and
// refused from then on, also after a restart.
//
// This ends the token the browser held when it logged out. A copy made
// before the session was last renewed is an older token and is not covered:
// ending every session of an account at once is a different rule (raising the
// account's password cycle), with the cost that logging out on one device
// logs out the others.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/golang-jwt/jwt"
)

const maxRevokedTokens = 5000

var revokedTokens = struct {
	sync.Mutex
	folder  string           // the folder the list was read from
	entries map[string]int64 // hash of the token -> its expiry (unix seconds)
}{}

func revokedTokensFile() string {
	return utils.CONFIGFOLDER + "revoked-sessions.json"
}

// loadRevokedTokens reads the list once per configuration folder. Must hold the lock.
func loadRevokedTokens() {
	if revokedTokens.entries != nil && revokedTokens.folder == utils.CONFIGFOLDER {
		return
	}
	revokedTokens.folder = utils.CONFIGFOLDER
	revokedTokens.entries = map[string]int64{}
	data, err := os.ReadFile(revokedTokensFile())
	if err != nil {
		return
	}
	stored := map[string]int64{}
	if json.Unmarshal(data, &stored) == nil {
		revokedTokens.entries = stored
	}
}

// pruneRevokedTokens forgets tokens that have expired by themselves. Must hold the lock.
func pruneRevokedTokens(now int64) {
	for hash, expiry := range revokedTokens.entries {
		if expiry <= now {
			delete(revokedTokens.entries, hash)
		}
	}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// revokePresentedToken is the hook of UserLogout: the token of the request,
// if it is one of ours and still valid, is not accepted again.
func revokePresentedToken(req *http.Request) {
	cookie, err := req.Cookie("jwttoken")
	if err != nil || cookie.Value == "" || len(cookie.Value) > 8192 {
		return
	}
	key, err := jwt.ParseEdPublicKeyFromPEM([]byte(utils.GetPublicAuthKey()))
	if err != nil {
		return
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(cookie.Value, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return key, nil
	})
	if err != nil || !token.Valid {
		return // not a token of this server, or expired already: nothing to remember
	}
	expiry, ok := claims["exp"].(float64)
	if !ok {
		return
	}

	revokedTokens.Lock()
	defer revokedTokens.Unlock()
	loadRevokedTokens()
	now := time.Now().Unix()
	pruneRevokedTokens(now)
	if len(revokedTokens.entries) >= maxRevokedTokens {
		utils.Warn("Logout: the list of logged-out sessions is full, this one is not remembered")
		return
	}
	revokedTokens.entries[tokenHash(cookie.Value)] = int64(expiry)

	data, err := json.Marshal(revokedTokens.entries)
	if err != nil {
		return
	}
	temporary := revokedTokensFile() + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		utils.Error("Logout: the logged-out session could not be saved", err)
		return
	}
	if err := os.Rename(temporary, revokedTokensFile()); err != nil {
		utils.Error("Logout: the logged-out session could not be saved", err)
	}
}

// tokenRevoked is the hook of RefreshUserToken, asked once the signature of
// the token has been checked.
func tokenRevoked(token string) bool {
	revokedTokens.Lock()
	defer revokedTokens.Unlock()
	loadRevokedTokens()
	expiry, found := revokedTokens.entries[tokenHash(token)]
	return found && expiry > time.Now().Unix()
}
