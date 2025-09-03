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
package cases

import (
	"flag"
	"testing"
)

var runSlowTests = flag.Bool("run-slow-tests", false, "Run slow tests")

func RunAllTests(t *testing.T, runner Runner) {
	runner.Setup(t)
	t.Run("SCPCommand", func(t *testing.T) {
		testScpCommand(t, runner)
	})
	t.Run("ImagesCommand", func(t *testing.T) {
		testImagesCommand(t, runner)
	})
	t.Run("InstanceClone", func(t *testing.T) {
		testInstanceClone(t, runner)
	})
	t.Run("Ubuntu", func(t *testing.T) {
		if !runner.Supports("ubuntu") {
			t.Skip("Ubuntu tests are not supported by this runner")
		}
		testUbuntu(t, runner)
	})
	t.Run("Batch", func(t *testing.T) {
		testBatch(t, runner)
	})
}
