package docker

// Fork addition (plan 43, review B1): one rule for masked values in an edit.
//
// An account without the credentials-read permission reads a container's
// variables as NAME=*** (api_getcontainers.go). Whatever it sends back to the
// edit route (the repair of a container that does not start, or any other
// editor) must not replace the real values by the mask: for such a caller a
// value that is exactly the mask means "keep the current value". A masked
// value for a variable the container does not have cannot be kept, and is
// refused. A caller who may read credentials got the real values, so what it
// sends is taken as it is.

import (
	"errors"
	"strings"
)

// envMask is what api_getcontainers.go shows in place of a value.
const envMask = "***"

// ErrMaskedUnknown: a masked value for a variable the container does not have.
var ErrMaskedUnknown = errors.New("a variable sent as *** does not exist in the container: its value cannot be kept")

// keepMaskedEnv answers the environment to apply: the values sent, with
// every masked one replaced by the container's current value when the
// caller may not read credentials.
func keepMaskedEnv(current []string, sent []string, mayReadCredentials bool) ([]string, error) {
	if mayReadCredentials {
		return sent, nil
	}
	now := map[string]string{}
	for _, kv := range current {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			now[parts[0]] = parts[1]
		}
	}
	out := make([]string, 0, len(sent))
	for _, kv := range sent {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[1] == envMask {
			value, ok := now[parts[0]]
			if !ok {
				return nil, ErrMaskedUnknown
			}
			out = append(out, parts[0]+"="+value)
			continue
		}
		out = append(out, kv)
	}
	return out, nil
}
