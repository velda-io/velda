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
//go:build darwin
// +build darwin

package term

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func IsTerminal(fd uintptr) bool {
	var termios unix.Termios
	err := unix.IoctlSetTermios(int(fd), syscall.TIOCGETA, &termios)
	return err == nil
}
