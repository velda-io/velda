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
//go:build linux
// +build linux

package term

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// This provides similar to golang.org/x/crypto/ssh/terminal,
// but provides more utilitity like OpenPty & Winsize with pixels.

func OpenPTY() (*os.File, *os.File, error) {
	// Open the master and slave ends of the PTY
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open /dev/ptmx: %w", err)
	}

	// Get the slave name
	slaveName, err := ptsname(master)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("failed to get slave name: %w", err)
	}

	// Unlock the PTY
	if err := unlockPTY(master); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("failed to unlock PTY: %w", err)
	}

	// Open the slave end
	slaveFD, err := syscall.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("failed to open slave PTY: %w", err)
	}
	slave := os.NewFile(uintptr(slaveFD), slaveName)

	return master, slave, nil
}

func unlockPTY(pty *os.File) error {
	var n uint32
	return ioctl(pty.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&n)))
}

func ptsname(pty *os.File) (string, error) {
	var n uint32
	err := ioctl(pty.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	return fmt.Sprintf("/dev/pts/%d", n), err
}

func SetWinsize(fd uintptr, cols, rows int, xpixel, ypixel int) error {
	ws := &Winsize{
		Row:    uint16(rows),
		Col:    uint16(cols),
		Xpixel: uint16(xpixel),
		Ypixel: uint16(ypixel),
	}
	return ioctl(uintptr(fd), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
}

func IsTerminal(fd uintptr) bool {
	var termios unix.Termios
	err := unix.IoctlSetTermios(int(fd), syscall.TCGETS, &termios)
	return err == nil
}
