//go:build !windows

package store

// Outside Windows there's no DPAPI. trashbox only runs on Windows; this
// build exists so the tests run in WSL.
func protect(b []byte) ([]byte, error)   { return append([]byte(nil), b...), nil }
func unprotect(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
