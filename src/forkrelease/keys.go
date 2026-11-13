package forkrelease

// Fork addition: the public keys a release of this fork must be signed with.
//
// A release is installed only when its signature file verifies under one of
// these keys; the server checks before it hands a download to the launcher
// and the launcher checks again before it unpacks. Both carry this file.
//
// Two slots, so a key can be replaced without locking installed servers out:
// put the new key into the free slot, release (every server then trusts
// both), and only in a later release remove the old one. The private halves
// are GitHub Actions secrets of the environment "release"
// (FORK_RELEASE_SIGNING_KEY_1 and _2); scripts/fork-signing-key.sh makes a
// key pair, stores the private half there and prints the line for this file.
//
// A slot holds the base64 of the 32-byte Ed25519 public key, or noReleaseKey.
// While both slots hold noReleaseKey nothing verifies: every update is
// refused, and the release workflow refuses to build a release.

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
)

const noReleaseKey = "NO-RELEASE-KEY"

var releaseKeys = [2]string{
	noReleaseKey, // slot 1
	noReleaseKey, // slot 2
}

// decodeKeys returns the keys of the slots that hold one. A slot that holds
// neither a key nor noReleaseKey is an error: a mistyped key must not pass
// for "no key".
func decodeKeys(slots []string) ([]ed25519.PublicKey, error) {
	keys := []ed25519.PublicKey{}
	for i, slot := range slots {
		if slot == noReleaseKey {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(slot)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("release key slot %d does not hold an Ed25519 public key", i+1)
		}
		for _, known := range keys {
			if known.Equal(ed25519.PublicKey(raw)) {
				return nil, fmt.Errorf("release key slot %d repeats another slot", i+1)
			}
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	return keys, nil
}

// compiledKeys are the keys this binary trusts. A build with a slot that
// cannot be read trusts none.
func compiledKeys() []ed25519.PublicKey {
	keys, err := decodeKeys(releaseKeys[:])
	if err != nil {
		return nil
	}
	return keys
}

// KeyCount is the number of release keys compiled into this binary.
func KeyCount() int {
	return len(compiledKeys())
}
