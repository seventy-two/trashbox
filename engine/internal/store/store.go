// Package store keeps trashbox's settings and secrets in its data folder
// (%LOCALAPPDATA%\trashbox, or $TRASHBOX_DATA).
//
// Secrets (refresh tokens, app passwords) are encrypted with Windows DPAPI,
// so only this Windows user on this PC can read them. On other systems they
// are stored as-is: that build exists only for tests.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Account kinds.
const (
	Outlook = "outlook"
	Gmail   = "gmail"
)

// Account is one mailbox.
type Account struct {
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

// Config is config.json. It holds nothing secret.
type Config struct {
	OutlookClientID string    `json:"outlookClientId,omitempty"`
	Accounts        []Account `json:"accounts"`
}

// Store is the data folder.
type Store struct{ Dir string }

// Open returns the store in $TRASHBOX_DATA or the user's local app data.
func Open() (*Store, error) {
	dir := os.Getenv("TRASHBOX_DATA")
	if dir == "" {
		base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(base, "trashbox")
	}
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// Load reads config.json; a missing file is an empty config.
func (s *Store) Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// Save writes config.json.
func (s *Store) Save(c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, "config.json"), append(b, '\n'))
}

// Add puts an account in the config, replacing one with the same address.
func (c *Config) Add(a Account) {
	a.Address = strings.ToLower(a.Address)
	for i, x := range c.Accounts {
		if x.Address == a.Address {
			c.Accounts[i] = a
			return
		}
	}
	c.Accounts = append(c.Accounts, a)
}

// Find returns the account with this address, or the only account of this
// kind when address is empty.
func (c Config) Find(kind, address string) (Account, error) {
	var matches []Account
	for _, a := range c.Accounts {
		if a.Kind == kind && (address == "" || a.Address == strings.ToLower(address)) {
			matches = append(matches, a)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) == 0 && address != "":
		return Account{}, fmt.Errorf("no %s account %s", kind, address)
	case len(matches) == 0:
		return Account{}, fmt.Errorf("no %s accounts yet", kind)
	default:
		return Account{}, fmt.Errorf("%d %s accounts: say which with -account", len(matches), kind)
	}
}

func (s *Store) secretPath(a Account) string {
	name := strings.NewReplacer("@", "_at_", "/", "_", `\`, "_", ":", "_").Replace(a.Address)
	return filepath.Join(s.Dir, "secrets", a.Kind+"-"+name+".bin")
}

// SetSecret stores an account's secret, encrypted.
func (s *Store) SetSecret(a Account, secret string) error {
	enc, err := protect([]byte(secret))
	if err != nil {
		return fmt.Errorf("encrypting secret: %w", err)
	}
	return writeAtomic(s.secretPath(a), enc)
}

// Secret reads an account's secret.
func (s *Store) Secret(a Account) (string, error) {
	enc, err := os.ReadFile(s.secretPath(a))
	if err != nil {
		return "", err
	}
	b, err := unprotect(enc)
	if err != nil {
		return "", fmt.Errorf("decrypting secret: %w", err)
	}
	return string(b), nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
