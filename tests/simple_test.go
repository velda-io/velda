//go:build simple

package tests

import (
	"testing"

	"velda.io/velda/tests/cases"
	"velda.io/velda/tests/runner"
)

func TestSimple(t *testing.T) {
	runner := runner.NewSimpleRunner()
	cases.RunAllTests(t, runner)
}
