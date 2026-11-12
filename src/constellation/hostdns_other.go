//go:build !linux

package constellation

import "errors"

func setHostDNS(adapter, dns string) error {
	return errors.New("setting the DNS of the server is only supported on Linux")
}

func resetHostDNS(adapter string) error {
	return nil
}

func waitForAdapter(adapter string) bool {
	return true
}
