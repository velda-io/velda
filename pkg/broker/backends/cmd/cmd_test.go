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
package cmd

import (
	"os"
	"testing"

	"velda.io/velda/pkg/broker/backends/backend_testing"
)

func TestCmdBackend(t *testing.T) {
	tmpdir := t.TempDir()
	backend := NewCmdPoolBackend(
		"basename $(mktemp "+tmpdir+"/worker-XXXXX)",
		"rm "+tmpdir+"/$1",
		"ls "+tmpdir,
		"",
	)
	// This tests always run because the Cmd backend is lightweight and has no external dependencies
	oldEnv := os.Getenv("BACKEND_TESTING")
	defer os.Setenv("BACKEND_TESTING", oldEnv)
	os.Setenv("BACKEND_TESTING", "1")
	backend_testing.TestSimpleScaleUpDown(t, backend)
}
