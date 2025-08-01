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
package utils

import (
	"io"
	"math/rand"
	"sync"
)

func RandString(n int) string {
	const letterBytes = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

type WriterWriterCloser interface {
	io.ReadWriteCloser
	CloseWrite() error
}

func CopyConnections(src, dst WriterWriterCloser) {
	defer src.Close()
	defer dst.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer dst.CloseWrite()
		io.Copy(dst, src)
		wg.Done()
	}()
	func() {
		defer src.CloseWrite()
		io.Copy(src, dst)
		wg.Done()
	}()
	wg.Wait()
}

type SessionKey struct {
	InstanceId int64
	SessionId  string
}

type ErrorWithExitCode interface {
	error
	ExitCode() int
}
