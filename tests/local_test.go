// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//go:build local

package tests

import (
	"flag"
	"testing"

	"velda.io/velda/tests/cases"
	"velda.io/velda/tests/runner"
)

var zfsRoot = flag.String("zfs-root", "zpool/tests", "ZFS root pool for local tests")

func TestLocal(t *testing.T) {
	runner := runner.NewLocalRunner(*zfsRoot)
	cases.RunAllTests(t, runner)
}

func TestMini(t *testing.T) {
	runner := runner.NewLocalMiniRunner(*zfsRoot)
	cases.RunAllTests(t, runner)
}

func TestMiniE2E(t *testing.T) {
	runner := runner.NewMiniE2ERunner()
	cases.RunAllTests(t, runner)
}
