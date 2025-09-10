package agent

import "os/exec"

type CommandModifier func(cmd *exec.Cmd)
