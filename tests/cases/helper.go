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
	"testing"

	testhelper "velda.io/velda/tests/testhelper"
)

// Local wrappers delegating to shared testhelper for compatibility with existing code.
func runVelda(args ...string) error { return testhelper.RunVelda(args...) }
func runVeldaWithOutput(args ...string) (string, error) {
	return testhelper.RunVeldaWithOutput(args...)
}
func runVeldaWithOutErr(args ...string) (string, string, error) {
	return testhelper.RunVeldaWithOutErr(args...)
}

type Feature string

const (
	FeatureImage           Feature = "image"
	FeatureSnapshot        Feature = "snapshot"
	FeatureMultiAgent      Feature = "multi-agent"
	FeatureBatchedSchedule Feature = "batched-schedule"
	FeatureZeroMaxPool     Feature = "zero-max-pool"
)

type Runner interface {
	Setup(t *testing.T)
	CreateTestInstance(t *testing.T, namePrefix string, image string) string
	Supports(feature Feature) bool
}
