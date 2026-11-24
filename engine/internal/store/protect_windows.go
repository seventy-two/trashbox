package store

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// entropy ties the encrypted blobs to trashbox, so another app running as
// the same user can't decrypt them with a bare CryptUnprotectData call.
var entropy = []byte("trashbox secrets v1")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func take(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func protect(b []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(b), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return take(&out), nil
}

func unprotect(b []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return take(&out), nil
}
