package certs

import (
	"crypto"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/go-acme/lego/v5/acme"

	"github.com/azukaar/cosmos-server/src/utils"
)

// configFolder is where the account and state files live. A variable for
// the tests.
var configFolder = func() string { return utils.CONFIGFOLDER }

// writePrivate writes a file only its owner can read, through a temporary
// file and a rename: a crash never leaves half a file where the account or
// the state was.
func writePrivate(path string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// loadAccount returns the account registered at the CA, nil when there is
// none or the file is for another directory.
func loadAccount(ca CA) *utils.ACMEAccount {
	content, err := os.ReadFile(filepath.Join(configFolder(), ca.accountFile()))
	if err != nil {
		return nil
	}
	account := utils.ACMEAccount{}
	if err := json.Unmarshal(content, &account); err != nil {
		utils.Warn("Certificates: ignoring the unreadable account file " + ca.accountFile())
		return nil
	}
	if account.Directory != ca.Directory || account.URL == "" {
		return nil
	}
	if _, err := account.PrivateKey(); err != nil {
		utils.Warn("Certificates: ignoring the account file " + ca.accountFile() + ", its key cannot be read")
		return nil
	}
	return &account
}

func saveAccount(ca CA, account utils.ACMEAccount) error {
	if account.Directory != ca.Directory {
		return errors.New("the account belongs to another directory")
	}
	content, err := json.MarshalIndent(account, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(configFolder(), ca.accountFile()), content)
}

// forgetAccount removes the account of a CA that no longer knows it; the
// next order registers a new one.
func forgetAccount(ca CA) {
	os.Remove(filepath.Join(configFolder(), ca.accountFile()))
}

// acmeUser is the account as lego wants it.
type acmeUser struct {
	email        string
	registration *acme.ExtendedAccount
	key          crypto.Signer
}

func (u *acmeUser) GetEmail() string                       { return u.email }
func (u *acmeUser) GetRegistration() *acme.ExtendedAccount { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.Signer           { return u.key }
