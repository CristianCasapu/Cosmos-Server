package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
)

// acmeAccountFile is where this server keeps its Let's Encrypt account, next
// to the rest of its config. Let's Encrypt allows a handful of new accounts
// per IP and hour, so the account is registered once and reused for every
// order after that. One per server: the limit is per IP, not per cluster.
const acmeAccountFile = "acme-account.json"

type ACMEAccount struct {
	// Directory is the CA the account was registered at: a staging account
	// is no use against production, and the other way round.
	Directory string `json:"directory"`
	URL       string `json:"url"`
	Email     string `json:"email"`
	// Key is the account's private key, PEM encoded.
	Key string `json:"key"`
}

func acmeAccountPath() string {
	return CONFIGFOLDER + acmeAccountFile
}

// LoadACMEAccount returns the account registered at the directory, nil when
// there is none yet (or it belongs to another CA).
func LoadACMEAccount(directory string) *ACMEAccount {
	content, err := os.ReadFile(acmeAccountPath())
	if err != nil {
		return nil
	}
	account := ACMEAccount{}
	if err := json.Unmarshal(content, &account); err != nil {
		Warn("ACME: ignoring the unreadable account file " + acmeAccountPath() + ": " + err.Error())
		return nil
	}
	if account.Directory != directory || account.URL == "" {
		return nil
	}
	if _, err := account.PrivateKey(); err != nil {
		Warn("ACME: ignoring the account file, its key cannot be read: " + err.Error())
		return nil
	}
	return &account
}

// SaveACMEAccount writes the account for the next orders to reuse
func SaveACMEAccount(account ACMEAccount) error {
	content, err := json.MarshalIndent(account, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(acmeAccountPath(), content, 0600)
}

func (a ACMEAccount) PrivateKey() (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(a.Key))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// NewACMEAccountKey generates the key of an account to register, PEM encoded
func NewACMEAccountKey() (*ecdsa.PrivateKey, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, "", err
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}
