// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package term

import (
	"syscall"
	"unsafe"

	sshterm "golang.org/x/crypto/ssh/terminal"
)

// This provides similar to golang.org/x/crypto/ssh/terminal,
// but provides more utilitity like OpenPty & Winsize with pixels.

type Winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func GetWinsize(fd uintptr) (*Winsize, error) {
	ws := Winsize{}
	err := ioctl(uintptr(fd), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	return &ws, err
}

func ioctl(fd, cmd, ptr uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, cmd, ptr)
	if errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}

var MakeRaw = sshterm.MakeRaw
var Restore = sshterm.Restore

type State = sshterm.State
