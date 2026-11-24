// Package winui holds the few bits of Windows UI the engine needs on its
// own: opening a URL in the default browser and the system credential prompt.
package winui

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OpenURL opens a URL in the default browser.
func OpenURL(u string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(u)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

var (
	credui                   = windows.NewLazySystemDLL("credui.dll")
	procPromptForCredentials = credui.NewProc("CredUIPromptForCredentialsW")
)

type creduiInfo struct {
	size    uint32
	parent  windows.HWND
	message *uint16
	caption *uint16
	banner  windows.Handle
}

const (
	flagDoNotPersist       = 0x2
	flagExcludeCerts       = 0x8
	flagAlwaysShowUI       = 0x80
	flagGenericCredentials = 0x40000
	maxUser                = 513
	maxPassword            = 256
)

// ErrCancelled means the user closed the prompt.
var ErrCancelled = errors.New("cancelled")

// PromptCredentials shows Windows' own username/password dialog. Nothing is
// saved by Windows; the caller decides what to keep.
func PromptCredentials(caption, message, user string) (string, string, error) {
	capt, _ := windows.UTF16PtrFromString(caption)
	msg, _ := windows.UTF16PtrFromString(message)
	info := creduiInfo{caption: capt, message: msg}
	info.size = uint32(unsafe.Sizeof(info))
	target, _ := windows.UTF16PtrFromString("trashbox")

	userBuf := make([]uint16, maxUser)
	copy(userBuf[:maxUser-1], windows.StringToUTF16(user))
	passBuf := make([]uint16, maxPassword)
	defer clear(passBuf)
	var save int32

	r, _, _ := procPromptForCredentials.Call(
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(target)), 0, 0,
		uintptr(unsafe.Pointer(&userBuf[0])), maxUser,
		uintptr(unsafe.Pointer(&passBuf[0])), maxPassword,
		uintptr(unsafe.Pointer(&save)),
		flagGenericCredentials|flagAlwaysShowUI|flagDoNotPersist|flagExcludeCerts,
	)
	switch windows.Errno(r) {
	case 0:
		return windows.UTF16ToString(userBuf), windows.UTF16ToString(passBuf), nil
	case windows.ERROR_CANCELLED:
		return "", "", ErrCancelled
	default:
		return "", "", windows.Errno(r)
	}
}
