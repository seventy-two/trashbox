//go:build !windows

package winui

import "errors"

// ErrCancelled means the user closed the prompt.
var ErrCancelled = errors.New("cancelled")

var errWindowsOnly = errors.New("only available on Windows")

// OpenURL is Windows-only.
func OpenURL(string) error { return errWindowsOnly }

// PromptCredentials is Windows-only.
func PromptCredentials(string, string, string) (string, string, error) {
	return "", "", errWindowsOnly
}
