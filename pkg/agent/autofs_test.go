// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package agent

import (
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutoFSMount(t *testing.T) {
	if os.Getenv("RUN_AUTOFS_TEST") != "1" {
		t.Skip("Skipping AutoFS mount test.")
	}

	mountPoint := "/tmp/autofstest"
	err := os.MkdirAll(mountPoint, 0755)
	assert.NoError(t, err)

	defer os.RemoveAll(mountPoint)

	autofs, err := NewAutoFS()
	assert.NoError(t, err)
	err = autofs.Run()
	assert.NoError(t, err)

	mount := func(_ string) error {
		return syscall.Mount("none", mountPoint, "tmpfs", syscall.MS_MGC_VAL, "")
	}

	err = autofs.Mount(mountPoint, mount)
	assert.NoError(t, err)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
