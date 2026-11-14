package configapi

// Fork addition: the fields of an API token are checked on the server. The
// IP restriction was stored as sent: an entry that is not an address (a typo,
// "192.168.1.0/24 " with a space) never matches, so the token silently works
// from nowhere, or the admin believes it is restricted when the list is
// garbage. The description was unbounded, the expiry and the permissions
// taken as they came.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxTokenIPs         = 32
	maxTokenDescription = 256
	maxTokenExpiryDays  = 3650
)

// tokenPermissions are the permissions a token can hold (utils/types.go).
var tokenPermissions = map[utils.Permission]bool{
	utils.PERM_ADMIN_READ: true, utils.PERM_ADMIN: true,
	utils.PERM_USERS_READ: true, utils.PERM_USERS: true,
	utils.PERM_RESOURCES_READ: true, utils.PERM_RESOURCES: true,
	utils.PERM_CONFIGURATION_READ: true, utils.PERM_CONFIGURATION: true,
	utils.PERM_CREDENTIALS_READ: true,
}

// checkTokenIPs trims the entries and refuses one that is not an address or a range.
func checkTokenIPs(list []string) ([]string, error) {
	cleaned := cleanList(list)
	if len(cleaned) > maxTokenIPs {
		return nil, fmt.Errorf("a token can be limited to at most %d addresses or ranges", maxTokenIPs)
	}
	for _, entry := range cleaned {
		if !validAddressEntry(entry) {
			return nil, errors.New("not an address or a range: " + plainText(entry) + ". Enter for example 192.168.1.20 or 192.168.1.0/24")
		}
	}
	return cleaned, nil
}

func checkTokenDescription(description string) error {
	if len(description) > maxTokenDescription {
		return fmt.Errorf("the description is at most %d characters", maxTokenDescription)
	}
	if strings.ContainsAny(description, "\r\n\x00") {
		return errors.New("the description is one line")
	}
	return nil
}

func checkTokenPermissions(permissions []utils.Permission) error {
	for _, p := range permissions {
		if !tokenPermissions[p] {
			return fmt.Errorf("unknown permission %d", p)
		}
	}
	return nil
}

// checkCreateToken checks a new token's fields and keeps its IP list trimmed.
func checkCreateToken(request *CreateAPITokenRequest) error {
	if err := checkTokenDescription(request.Description); err != nil {
		return err
	}
	if request.ExpiryDays < 0 || request.ExpiryDays > maxTokenExpiryDays {
		return fmt.Errorf("the expiry is from 0 (never) to %d days", maxTokenExpiryDays)
	}
	if err := checkTokenPermissions(request.Permissions); err != nil {
		return err
	}
	ips, err := checkTokenIPs(request.IPWhitelist)
	if err != nil {
		return err
	}
	request.IPWhitelist = ips
	return nil
}

// checkUpdateToken checks the fields an update carries.
func checkUpdateToken(request *UpdateAPITokenRequest) error {
	if request.Description != nil {
		if err := checkTokenDescription(*request.Description); err != nil {
			return err
		}
	}
	if err := checkTokenPermissions(request.Permissions); err != nil {
		return err
	}
	if request.IPWhitelist != nil {
		ips, err := checkTokenIPs(request.IPWhitelist)
		if err != nil {
			return err
		}
		request.IPWhitelist = ips
	}
	return nil
}
