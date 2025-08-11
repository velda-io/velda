package cases

import "testing"

func RunAllTests(t *testing.T, runner Runner) {
	runner.Setup(t)
	defer runner.TearDown(t)
	t.Run("SCPCommand", testScpCommand)
}
