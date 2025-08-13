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
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "unsafe"

	"golang.org/x/sys/unix"
	pb "google.golang.org/protobuf/proto"
	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type ErrorCheckPointed struct {
}

func (e *ErrorCheckPointed) Error() string {
	return "Process was checkpointed"
}

func (e *ErrorCheckPointed) ExitCode() int {
	return 100
}

type RunPid1Plugin struct {
	PluginBase
	requestPlugin   interface{}
	WorkspaceDir    string
	AppArmorProfile string
	ChanCheckPoint  chan *proto.CheckPointRequest
}

func NewRunPid1Plugin(workspaceDir string, sandboxCfg *agentpb.SandboxConfig, agentDaemonPlugin *AgentDaemonPlugin, requestPlugin interface{}) *RunPid1Plugin {
	return &RunPid1Plugin{
		WorkspaceDir:    workspaceDir,
		AppArmorProfile: sandboxCfg.GetApparmorProfile(),
		ChanCheckPoint:  agentDaemonPlugin.ChanCheckPoint,
		requestPlugin:   requestPlugin,
	}
}

func (p *RunPid1Plugin) Run(ctx context.Context) error {
	request := ctx.Value(p.requestPlugin).(*proto.SessionRequest)
	var cmd *os.Process
	var err error
	if !request.Checkpointed {
		cmd, err = p.runPid1(request)
	} else {
		cmd, err = p.performRestore(request)
	}
	if err != nil {
		return fmt.Errorf("Failed to run pid1: %w", err)
	}

	var checkPoint bool
	completion := make(chan *os.ProcessState, 1)
	sigfinish := make(chan struct{})
	defer close(sigfinish)
	sigtermChan := make(chan os.Signal, 1)
	signal.Notify(sigtermChan, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		for {
			select {
			case sig := <-sigtermChan:
				if sig == syscall.SIGQUIT {
					err := killWithCgroup()
					log.Printf("Received SIGQUIT, killing with cgroup: %v", err)
				} else if cmd != nil {
					cmd.Signal(sig)
				}
			case <-sigfinish:
				return
			}
		}
	}()
	go func() {
		ps, err := cmd.Wait()
		if err != nil {
			close(completion)
			log.Printf("pid1 exited with error: %v", err)
		} else {
			completion <- ps
		}
	}()
	for {
		select {
		case checkPointRequest := <-p.ChanCheckPoint:
			err := p.performCheckpoint(cmd.Pid, request)
			if err != nil {
				log.Printf("Failed to perform checkpoint: %v", err)
				if checkPointRequest.FailSessionOnCheckpointFailure {
					cmd.Signal(syscall.SIGTERM)
					log.Printf("Checkpoint failed, terminating pid1")
				}
				// Continue the original execution
				continue
			}
			log.Printf("Checkpoint performed successfully")
			checkPoint = true
		case state, ok := <-completion:
			if !ok {
				log.Printf("pid1 process state channel closed")
				return fmt.Errorf("pid1 process state channel closed unexpectedly")
			}
			if checkPoint {
				return &ErrorCheckPointed{}
			}
			returnCode := state.ExitCode()
			if returnCode != 0 {
				return fmt.Errorf("pid1 exited with code %d", returnCode)
			}
			return nil
		}
	}
}

func currentCgroupPath() (string, error) {
	cgroupBasePath := "/sys/fs/cgroup"
	cgFile := "/proc/self/cgroup"
	data, err := os.ReadFile(cgFile)
	if err != nil {
		return "", fmt.Errorf("failed to read cgroup file: %w", err)
	}
	cgroupDir := ""
	lines := string(data)
	for _, line := range strings.Split(lines, "\n") {
		// cgroup v2 has only one line like: 0::/some/path
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" {
			cgroupDir = parts[2]
			break
		}
	}
	if cgroupDir == "" {
		return "", fmt.Errorf("could not determine cgroup v2 path")
	}
	return fmt.Sprintf("%s%s", cgroupBasePath, cgroupDir), nil
}

func killWithCgroup() error {
	cgPath, err := currentCgroupPath()
	if err != nil {
		return fmt.Errorf("failed to get current cgroup path: %w", err)
	}
	cgKillPath := fmt.Sprintf("%s/workload/cgroup.kill", cgPath)
	err = os.WriteFile(cgKillPath, []byte("1"), 0644)
	if err != nil {
		return fmt.Errorf("failed to write to cgroup kill file: %w", err)
	}
	return nil
}

func extractCgroupFD() (int, error) {
	cgPath, err := currentCgroupPath()
	if err != nil {
		return -1, fmt.Errorf("failed to get current cgroup path: %w", err)
	}
	newCgroupPath := fmt.Sprintf("%s/workload", cgPath)
	if err := os.MkdirAll(newCgroupPath, 0755); err != nil {
		return -1, fmt.Errorf("failed to create new cgroup: %w", err)
	}
	fd, err := syscall.Open(newCgroupPath, syscall.O_DIRECTORY|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("failed to open new cgroup dir: %w", err)
	}
	return fd, nil
}

func saveFilesToInherit(pid int, checkpointDir string, fds []int) ([]string, error) {
	mappedFiles := make([]string, 0, 4)
	fdDir := fmt.Sprintf("/proc/%d/map_files", pid)
	// All entries should be pointing to the velda binary.
	entries, err := os.ReadDir(fdDir)
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("failed to find mapped file of binary %s: %w", fdDir, err)
	}
	paths := make([]string, 0, 1+len(fds))
	paths = append(paths, fmt.Sprintf("%s/%s", fdDir, entries[0].Name()))
	for _, fd := range fds {
		paths = append(paths, fmt.Sprintf("/proc/%d/fd/%d", pid, fd))
	}
	for _, fdPath := range paths {
		fd, err := syscall.Open(fdPath, unix.O_PATH, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to open fd file %s: %w", fdPath, err)
		}
		defer unix.Close(fd)

		fdinfoPath := fmt.Sprintf("/proc/self/fdinfo/%d", fd)
		fdinfoBytes, err := os.ReadFile(fdinfoPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read fdinfo %s: %w", fdinfoPath, err)
		}
		var mntid, ino string
		lines := strings.Split(string(fdinfoBytes), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "mnt_id:") {
				mntid = strings.TrimSpace(strings.TrimPrefix(line, "mnt_id:"))
			}
			if strings.HasPrefix(line, "ino:") {
				ino = strings.TrimSpace(strings.TrimPrefix(line, "ino:"))
			}
			if mntid != "" && ino != "" {
				break
			}
		}
		if mntid == "" || ino == "" {
			return nil, fmt.Errorf("could not extract mntid or ino from fdinfo %s for file %s", fdinfoPath, fdPath)
		}
		mntidInt, err := strconv.ParseUint(mntid, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse mntid %s: %w", mntid, err)
		}
		inoInt, err := strconv.ParseUint(ino, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ino %s: %w", ino, err)
		}
		var key string
		stat, err := os.Stat(fdPath)
		if (stat.Mode() & os.ModeNamedPipe) != 0 {
			key = fmt.Sprintf("pipe:[%d]", inoInt)
		} else {
			key = fmt.Sprintf("file[%x:%x]", mntidInt, inoInt)
		}
		mappedFiles = append(mappedFiles, key)
	}
	if err := saveMappedFile(mappedFiles, checkpointDir); err != nil {
		return nil, fmt.Errorf("failed to save mapped files: %w", err)
	}
	return mappedFiles, nil
}

func saveMappedFile(keys []string, checkpointDir string) error {
	filePath := path.Join(checkpointDir, "external_files.txt")
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create external files list: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString("version=0\n")
	if err != nil {
		return fmt.Errorf("failed to write version to external files list: %w", err)
	}

	for _, key := range keys {
		_, err := file.WriteString(key + "\n")
		if err != nil {
			return fmt.Errorf("failed to write key to external files list: %w", err)
		}
	}

	return nil
}

func loadMappedFile(checkpointDir string) ([]string, error) {
	filePath := path.Join(checkpointDir, "external_files.txt")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read external files list: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || lines[0] != "version=0" {
		return nil, fmt.Errorf("invalid external files list format")
	}

	keys := lines[1:] // Skip the version line
	// Remove any empty lines at the end
	keys = keys[:len(keys)-1]

	return keys, nil
}

func setApparmorProfile(profile string) error {
	if profile == "" {
		return nil
	}
	f, err := os.OpenFile(fmt.Sprintf("/proc/%d/task/%d/attr/apparmor/exec", os.Getpid(), unix.Gettid()), os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("Failed to open AppArmor exec file: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString("exec " + profile)
	if err != nil {
		return fmt.Errorf("Failed to set AppArmor profile: %w", err)
	}
	return nil
}

func (p *RunPid1Plugin) runPid1(request *proto.SessionRequest) (*os.Process, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if p.AppArmorProfile != "" {
		if err := setApparmorProfile(p.AppArmorProfile); err != nil {
			return nil, err
		}
	}
	executable, _ := os.Executable()
	args := os.Args[1:]
	args = append(args, "--pid1")
	requestBytes, err := pb.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal request: %w", err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("Failed to create stdin pipe: %w", err)
	}
	go func() {
		defer stdinW.Close()
		stdinW.Write(requestBytes)
	}()
	// Setup CGroup.
	fd, err := extractCgroupFD()
	if err != nil {
		return nil, fmt.Errorf("Failed to extract cgroup fd: %w", err)
	}
	files := []*os.File{stdinR, os.Stdout, os.Stderr}
	if request.Workload != nil {
		files = append(files, os.NewFile(3, "/proc/self/fd/3")) // Batch output
	}
	cmd, err := os.StartProcess(executable, append([]string{executable}, args...), &os.ProcAttr{
		Files: files,
		Env:   os.Environ(),
		Sys: &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWCGROUP,
			Setsid:      true,
			CgroupFD:    fd,
			UseCgroupFD: true,
		},
	})
	syscall.Close(fd)
	stdinR.Close()
	if err != nil {
		return nil, fmt.Errorf("Failed to start pid1: %w", err)
	}
	return cmd, nil
}

func (p *RunPid1Plugin) performCheckpoint(pid int, request *proto.SessionRequest) error {
	log.Printf("Starting checkpointing process")
	checkPointDir := path.Join(p.WorkspaceDir, "workspace", ".velda_cp", request.SessionId)
	if err := os.MkdirAll(checkPointDir, 0755); err != nil {
		return fmt.Errorf("Failed to create checkpoint directory: %w", err)
	}
	// Get inode and mntid of /velda & stdin/stdout/stderr
	fds := []int{1, 2} // stdin, stdout, stderr
	if request.Workload != nil {
		fds = append(fds, 3) // Batch output
	}
	externalFiles, err := saveFilesToInherit(pid, checkPointDir, fds)
	if err != nil {
		return fmt.Errorf("Failed to open all mapped files: %w", err)
	}
	args := []string{
		"dump",
		"-t", fmt.Sprintf("%d", pid),
		"-D", checkPointDir,
		"-v4",
		"--log-file", "dump.log",
		"--tcp-close",
		"--external", "mnt[]:m",
		"--link-remap",
	}
	for _, key := range externalFiles {
		args = append(args, "--external", key)
	}
	criuCmd := exec.Command("criu", args...)
	criuCmd.Stdout = os.Stdout
	criuCmd.Stderr = os.Stderr
	if err := criuCmd.Run(); err != nil {
		return fmt.Errorf("Failed to invoke criu dump: %w", err)
	}
	return nil
}

func (p *RunPid1Plugin) performRestore(request *proto.SessionRequest) (*os.Process, error) {
	checkPointDir := path.Join(p.WorkspaceDir, "workspace", ".velda_cp", request.SessionId)
	ts := time.Now().Unix()
	if _, err := os.Stat(checkPointDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Checkpoint directory does not exist: %s\n", checkPointDir)
		return nil, fmt.Errorf("Checkpoint directory does not exist: %w", err)
	}
	fds := make([]*os.File, 6, 7)
	fds[0] = os.Stdin
	fds[1] = os.Stdout
	fds[2] = os.Stderr

	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("Failed to get executable path: %w", err)
	}

	fds[3], err = os.OpenFile(executable, unix.O_PATH, 0)
	if err != nil {
		return nil, fmt.Errorf("Failed to open executable file: %w", err)
	}
	fds[4] = os.Stdout
	fds[5] = os.Stderr
	if request.Workload != nil {
		fds = append(fds, os.NewFile(3, "/proc/self/fd/3")) // Batch output
	}

	externalFiles, err := loadMappedFile(checkPointDir)
	if err != nil || len(externalFiles)+3 != len(fds) {
		return nil, fmt.Errorf("Failed to load mapped files: %w", err)
	}

	restoreArgs := []string{
		"criu",
		"restore",
		"-D", checkPointDir,
		"-v4",
		"--log-file", fmt.Sprintf("restore-%d.log", ts),
		"--tcp-close",
		"--external", "mnt[]:m",
		"--root", path.Join(p.WorkspaceDir, "workspace"),
		"--pidfile", fmt.Sprintf("pid-%d", ts),
		"--link-remap",
		"-d", "-S",
	}
	// Prepare the inherit file descriptors

	for i, key := range externalFiles {
		restoreArgs = append(restoreArgs, "--inherit-fd", fmt.Sprintf("fd[%d]:%s", i+3, key))
	}
	criuPath, err := exec.LookPath("criu")
	if err != nil {
		return nil, fmt.Errorf("Failed to find criu executable: %w", err)
	}
	restoreCmd, err := os.StartProcess(criuPath, restoreArgs, &os.ProcAttr{
		Files: fds,
		Env:   os.Environ(),
	})

	if err != nil {
		return nil, fmt.Errorf("Failed to start criu restore: %w", err)
	}
	restoreResult, err := restoreCmd.Wait()
	if err != nil || restoreResult.ExitCode() != 0 {
		return nil, fmt.Errorf("Criu restore failed with exit code %d: %w", restoreResult.ExitCode(), err)
	}
	// Find the PID of the restored process
	pidData, err := os.ReadFile(path.Join(checkPointDir, fmt.Sprintf("pid-%d", ts)))
	if err != nil {
		return nil, fmt.Errorf("Failed to read pid file: %w", err)
	}
	pidStr := strings.TrimSpace(string(pidData))
	restoredPid, err := strconv.Atoi(pidStr)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse restored pid: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Restored process PID: %d\n", restoredPid)

	restoredProc, err := os.FindProcess(restoredPid)
	if err != nil {
		return nil, fmt.Errorf("Failed to find restored process %d: %w", restoredPid, err)
	}
	if err := restoredProc.Signal(syscall.SIGCONT); err != nil {
		return nil, fmt.Errorf("Failed to notify restored process: %w", err)
	}
	return restoredProc, nil
}
