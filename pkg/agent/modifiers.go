package agent

import "os/exec"

// CommandModifier is a function type that modifies an exec.Cmd before execution,
// typically used to set environment variables or other execution parameters.
type CommandModifier func(cmd *exec.Cmd)

func gpuModifier(libraryPath, binPath string) func(*exec.Cmd) {
	return func(cmd *exec.Cmd) {
		existingLdLibraryPath := ""
		existingPath := ""

		for _, env := range cmd.Env {
			if len(env) > 15 && env[:15] == "LD_LIBRARY_PATH=" {
				existingLdLibraryPath = env[16:]
			}
			if len(env) > 5 && env[:5] == "PATH=" {
				existingPath = env[5:]
			}
		}

		if existingLdLibraryPath != "" {
			libraryPath = libraryPath + ":" + existingLdLibraryPath
		}

		if existingPath != "" {
			binPath = binPath + ":" + existingPath
		}
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+libraryPath)
		cmd.Env = append(cmd.Env, "PATH="+binPath)
	}
}
