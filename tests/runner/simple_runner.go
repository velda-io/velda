package runner

import "testing"

// Require pre-configure the environment manually when running the tests.
type SimpleRunner struct {
}

func NewSimpleRunner() *SimpleRunner {
	return &SimpleRunner{}
}

func (r *SimpleRunner) Setup(t *testing.T) {
}

func (r *SimpleRunner) TearDown(t *testing.T) {
}
