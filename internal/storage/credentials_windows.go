//go:build windows

package storage

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var advapi = windows.NewLazySystemDLL("advapi32.dll")
var credWrite = advapi.NewProc("CredWriteW")
var credRead = advapi.NewProc("CredReadW")
var credDelete = advapi.NewProc("CredDeleteW")
var credFree = advapi.NewProc("CredFree")

// Native CREDENTIALW; credentials are protected by Windows Credential Manager,
// scoped to the signed-in Windows user, and never placed in settings or logs.
type credential struct {
	Flags          uint32
	Type           uint32
	TargetName     *uint16
	Comment        *uint16
	LastWritten    windows.Filetime
	BlobSize       uint32
	Blob           *byte
	Persist        uint32
	AttributeCount uint32
	Attributes     uintptr
	TargetAlias    *uint16
	UserName       *uint16
}
type Vault struct{ Target string }

func SessionVault() Vault { return Vault{Target: "Discrud/session"} }
func (v Vault) Save(token string) error {
	if len(token) == 0 || len(token) > 2560 {
		return errors.New("invalid credential size")
	}
	target, err := windows.UTF16PtrFromString(v.Target)
	if err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("Discord user session")
	data := []byte(token)
	defer clear(data)
	c := credential{Type: 1, TargetName: target, BlobSize: uint32(len(data)), Blob: &data[0], Persist: 2, UserName: name}
	ok, _, e := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0)
	runtime.KeepAlive(c)
	runtime.KeepAlive(data)
	if ok == 0 {
		return errors.New("Windows Credential Manager could not save the session: " + e.Error())
	}
	return nil
}
func (v Vault) Load() (string, error) {
	target, err := windows.UTF16PtrFromString(v.Target)
	if err != nil {
		return "", err
	}
	var p *credential
	ok, _, e := credRead.Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&p)))
	runtime.KeepAlive(target)
	if ok == 0 {
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return "", nil
		}
		return "", errors.New("Windows Credential Manager could not read the session: " + e.Error())
	}
	defer credFree.Call(uintptr(unsafe.Pointer(p)))
	if p == nil || p.BlobSize == 0 || p.BlobSize > 2560 {
		return "", errors.New("saved session is invalid")
	}
	return string(unsafe.Slice(p.Blob, int(p.BlobSize))), nil
}
func (v Vault) Delete() error {
	target, err := windows.UTF16PtrFromString(v.Target)
	if err != nil {
		return err
	}
	ok, _, e := credDelete.Call(uintptr(unsafe.Pointer(target)), 1, 0)
	runtime.KeepAlive(target)
	if ok == 0 && !errors.Is(e, windows.ERROR_NOT_FOUND) {
		return errors.New("Windows Credential Manager could not remove the session: " + e.Error())
	}
	return nil
}
