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
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/durationpb"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:     "run [flags] [command] [args...]",
	Example: "velda run -it less path-to-file",
	Short:   "Run a command in a session",
	Long: `Run a command in a session.

If no command is provided, it will start an interactive shell session.

Session is the basic unit of execution in Velda.  It is a collection of
pre-defined compute resources that can be independently attached your
instance. You can run multiple sessions with the same instsance in parallel.


If a session ID is provided, it will attach to the an existing session, or
create a new one if not already exists.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var returnCode int
		err := runCommand(cmd, args, &returnCode)
		if err != nil {
			return err
		}
		os.Exit(returnCode)
		return nil
	},
}

func runCommand(cmd *cobra.Command, args []string, returnCode *int) error {
	conn, err := clientlib.GetApiConnection()
	if err != nil {
		return fmt.Errorf("Error getting API connection: %v", err)
	}
	defer conn.Close()

	instance := cmd.Flag("instance").Value.String()
	instanceId, err := clientlib.ParseInstanceId(
		cmd.Context(),
		instance, clientlib.FallbackToSession)
	if err != nil {
		return err
	}

	brokerClient, err := clientlib.GetBrokerClient()
	if err != nil {
		return err
	}

	defaultShell := len(args) == 0
	var user string
	if clientlib.IsInSession() && !defaultShell {
		user = "root"
	} else {
		user, _ = cmd.Flags().GetString("user")
	}
	sessionId, _ := cmd.Flags().GetString("session-id")
	serviceName, _ := cmd.Flags().GetString("service-name")
	if sessionId != "" && serviceName != "" {
		return fmt.Errorf("Cannot specify both session-id and service-name")
	}
	if !cmd.Flag("service-name").Changed && !clientlib.IsInSession() {
		DebugLog("Defaulting service-name to ssh")
		serviceName = "ssh"
	}

	sessionReq := &proto.SessionRequest{
		ServiceName: serviceName,
		SessionId:   sessionId,
		InstanceId:  instanceId,
		Pool:        cmd.Flag("pool").Value.String(),
		User:        user,
	}
	sessionReq.ForceNewSession, _ = cmd.Flags().GetBool("new-session")
	sessionReq.Labels, _ = cmd.Flags().GetStringSlice("labels")
	checkPoint, _ := cmd.Flags().GetBool("checkpoint-on-idle")
	keepAlive, _ := cmd.Flags().GetBool("keep-alive")
	keepAliveTime, _ := cmd.Flags().GetDuration("keep-alive-time")
	if checkPoint {
		sessionReq.ConnectionFinishAction = proto.SessionRequest_CONNECTION_FINISH_ACTION_CHECKPOINT
		if keepAliveTime == 0 {
			// default to 10 seconds to start check-pointing after idle
			keepAliveTime = 10 * time.Second
		}
		sessionReq.IdleTimeout = durationpb.New(keepAliveTime)
	} else if keepAlive {
		sessionReq.ConnectionFinishAction = proto.SessionRequest_CONNECTION_FINISH_ACTION_KEEP_ALIVE
	} else {
		sessionReq.ConnectionFinishAction = proto.SessionRequest_CONNECTION_FINISH_ACTION_TERMINATE
		sessionReq.IdleTimeout = durationpb.New(keepAliveTime)
	}

	batch, _ := cmd.Flags().GetBool("batch")
	if batch {
		if !clientlib.IsInSession() {
			return errors.New("Batch mode is only available in Velda session")
		}
		workload, err := getWorkload(cmd, args)
		if err != nil {
			return err
		}
		sessionReq.TaskId = cmd.Flag("name").Value.String()
		sessionReq.Workload = workload
		after, _ := cmd.Flags().GetStringSlice("after")
		for _, task := range after {
			sessionReq.Dependencies = append(sessionReq.Dependencies, &proto.Dependency{
				UpstreamTaskId: task,
				Type:           proto.Dependency_DEPENDENCY_TYPE_UNSPECIFIED,
			})
		}
		afterSuccess, _ := cmd.Flags().GetStringSlice("after-success")
		for _, task := range afterSuccess {
			sessionReq.Dependencies = append(sessionReq.Dependencies, &proto.Dependency{
				UpstreamTaskId: task,
				Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
			})
		}
		afterFail, _ := cmd.Flags().GetStringSlice("after-fail")
		for _, task := range afterFail {
			sessionReq.Dependencies = append(sessionReq.Dependencies, &proto.Dependency{
				UpstreamTaskId: task,
				Type:           proto.Dependency_DEPENDENCY_TYPE_FAILURE,
			})
		}
	}

	DebugLog("Sending session request")

	resp, err := brokerClient.RequestSession(cmd.Context(), sessionReq)
	if err != nil {
		return err
	}
	DebugLog("Got response: %s. Connecting ", resp.String())
	if batch {
		fmt.Printf("Task https://sandbox.velda.team/tasks/%s submitted\n", resp.GetTaskId())
		return nil
	}

	client, err := clientlib.SshConnect(cmd, resp.GetSshConnection(), user)
	if err != nil {
		return fmt.Errorf("Error connecting to SSH: %v", err)
	}
	defer client.Close()

	DebugLog("Connected to SSH")
	attachAllForwardedPorts(cmd, client)

	session, reqs, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("Error opening channel: %v", err)
	}
	defer session.Close()
	DebugLog("Opened channel")

	ttymode, _ := cmd.Flags().GetString("tty")
	tty := false
	if ttymode == "auto" {
		tty = isatty.IsTerminal(os.Stdin.Fd())
	} else if ttymode == "yes" || ttymode == "true" {
		tty = true
	} else if ttymode == "no" || ttymode == "false" {
		tty = false
	} else {
		return fmt.Errorf("Invalid tty mode: %s", ttymode)
	}
	noinput, _ := cmd.Flags().GetBool("noinput")
	interactive := !noinput || tty || defaultShell

	if interactive {
		session.Stdin = os.Stdin
	}
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	if tty || defaultShell {
		if err := session.RequestPty(true); err != nil {
			return fmt.Errorf("Error requesting pty: %v", err)
		}
	}

	if defaultShell {
		if err := session.Shell(); err != nil {
			return fmt.Errorf("Error starting shell: %v", err)
		}
	} else if clientlib.IsInSession() {
		DebugLog("Use current shell environment")
		workload, err := getWorkload(cmd, args)
		if err != nil {
			return err
		}
		if err := session.ExecVelda(workload); err != nil {
			return fmt.Errorf("Error executing command: %v", err)
		}
	} else {
		DebugLog("Running from default env")
		// Join arg with space
		command := strings.Join(escapeArgsForShell(args), " ")
		DebugLog("Running command: %s", command)
		if err := session.Exec(command); err != nil {
			return fmt.Errorf("Error executing command: %v", err)
		}
	}

	// Wait for the exit status
	for {
		req := <-reqs
		if req == nil {
			*returnCode = -1
			if client.ShutdownMessage != "" {
				return fmt.Errorf("Session terminated: %s", client.ShutdownMessage)
			}
			break
		}
		switch req.Type {
		case "exit-status":
			var status struct {
				Status uint32
			}
			if err := ssh.Unmarshal(req.Payload, &status); err != nil {
				*returnCode = -1
				return fmt.Errorf("Error unmarshalling exit-status: %v", err)
			}
			*returnCode = int(status.Status)
			return nil
		case "exit-signal":
			*returnCode = -1
			return nil
		}
	}
	return nil
}

func getWorkload(cmd *cobra.Command, args []string) (*proto.Workload, error) {
	cwd, _ := cmd.Flags().GetString("directory")
	if cwd == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("Error getting working directory: %v", err)
		}
		cwd = workingDir
	}
	shell, _ := cmd.Flags().GetBool("shell")
	var command string
	var argv []string
	if shell {
		command = strings.Join(args, " ")
		argv = []string{}
	} else {
		command = args[0]
		argv = args[1:]
	}
	uid := syscall.Getuid()
	gid := syscall.Getgid()
	groups, err := syscall.Getgroups()
	if err != nil {
		log.Printf("Error getting groups: %v", err)
		// Fallback to empty groups
	}
	groupsUint32 := make([]uint32, 0, len(groups))
	for _, group := range groups {
		groupsUint32 = append(groupsUint32, uint32(group))
	}
	environs := os.Environ()
	environs = append(environs, removeLocalEnvs()...)
	commandPath, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("Error finding command path: %v", err)
	}

	return &proto.Workload{
		Command:     command,
		CommandPath: commandPath,
		Args:        argv,
		WorkingDir:  cwd,
		Environs:    environs,
		Shell:       shell,
		Uid:         uint32(uid),
		Gid:         uint32(gid),
		Groups:      groupsUint32,
	}, nil
}

// escapeArgsForShell escapes command line arguments to be safely used in shell commands.
// It handles special characters and spaces to prevent shell injection.
func escapeArgsForShell(args []string) []string {
	escaped := make([]string, len(args))
	for i, arg := range args {
		// If the argument is empty, use empty quotes
		if arg == "" {
			escaped[i] = "''"
			continue
		}

		// Check if the argument needs escaping
		needsEscaping := false
		for _, c := range arg {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
				c == '/' || c == '.' || c == '_' || c == '-' || c == '+' || c == '=' || c == ':' || c == ',') {
				needsEscaping = true
				break
			}
		}

		// If no special characters, use as is
		if !needsEscaping {
			escaped[i] = arg
			continue
		}

		// For arguments with special characters, use single quotes
		// Replace any single quotes in the argument with '\'' (close quote, escaped quote, open quote)
		escapedArg := strings.ReplaceAll(arg, "'", "'\\''")
		escaped[i] = "'" + escapedArg + "'"
	}
	return escaped
}

func removeLocalEnvs() []string {
	libraryPaths := strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":")
	binPath := strings.Split(os.Getenv("PATH"), ":")
	output := []string{}
	for i, path := range libraryPaths {
		if path == "/var/nvidia/lib" {
			libraryPaths = append(libraryPaths[:i], libraryPaths[i+1:]...)
			output = append(output, fmt.Sprint("LD_LIBRARY_PATH=", strings.Join(libraryPaths, ":")))
			break
		}
	}
	for i, path := range binPath {
		if path == "/var/nvidia/bin" {
			binPath = append(binPath[:i], binPath[i+1:]...)
			output = append(output, fmt.Sprint("PATH=", strings.Join(binPath, ":")))
			break
		}
	}
	return output
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringP("pool", "P", "shell", "Pool to run the workload in")
	runCmd.Flags().String("instance", "", "Instance name or ID. Default to the current instance if running in Velda, or default-instance config.")
	runCmd.Flags().String("tty", "auto", "TTy mode. auto|yes|no. Default to auto, will be based on input.")
	runCmd.Flags().BoolP("noinput", "I", false, "Don't take input from stdin. Only used with a command.")
	runCmd.Flags().Bool("shell", false, "Use shell")
	runCmd.Flags().Bool("batch", false, "Batch mode. Return immediately")
	runCmd.Flags().Bool("new-session", false, "Always create a new session")
	runCmd.Flags().Bool("keep-alive", false, "Keep the session alive after all processes exits even if all connections are closed.")
	runCmd.Flags().Bool("checkpoint-on-idle", false, "If all connections are closed, check-point the session. Imply keep-alive.")
	runCmd.Flags().StringP("service-name", "s", "", "Service name, which can be used to identify session later. Default is ssh if connected externally, or empty.")
	runCmd.Flags().String("session-id", "", "Reconnect to specific session. If not provided, it will try to find a session with the service name or create a new session.")
	runCmd.Flags().StringP("directory", "C", "", "Working directory. Default to current directory if running in Velda and a command is provided, otherwise home directory.")
	runCmd.Flags().StringP("user", "u", "user", "User to run the command as. Ignored if running in a Velda session with a command provided, where it will always use the current user.")
	runCmd.Flags().StringP("name", "n", "", "Name of the the task")
	runCmd.Flags().StringArrayP("publish", "p", nil, "Publish a session's port to the current session. Format: [host:]local_port:session_port")
	runCmd.Flags().StringSlice("after", nil, "For batch task, run it after other tasks are finished(either succeeded or failed)")
	runCmd.Flags().StringSlice("after-success", nil, "For batch task, run it after other tasks finished successfully (return 0)")
	runCmd.Flags().StringSlice("after-fail", nil, "For batch task, run it after other tasks are failed")
	runCmd.Flags().StringSliceP("labels", "l", nil, "Labels for the task")
	runCmd.Flags().Duration("keep-alive-time", 0, "How long to keep the session alive after all connections are closed. Default to 0, which means no keep-alive.")
	runCmd.Flags().SetInterspersed(false)
}

func attachAllForwardedPorts(cmd *cobra.Command, client *clientlib.SshClient) {
	forwardedPorts, _ := cmd.Flags().GetStringArray("publish")
	for _, forwardedPort := range forwardedPorts {
		parts := strings.Split(forwardedPort, ":")
		if len(parts) == 2 {
			parts = append([]string{"localhost"}, parts...)
		} else if len(parts) != 3 {
			log.Printf("Invalid port forwarding format: %s", forwardedPort)
			continue
		}
		host := parts[0]
		localPort := parts[1]
		remotePort := parts[2]
		if host == "" {
			host = "localhost"
		}
		if localPort == "" {
			localPort = "0"
		}
		remotePortInt, err := strconv.Atoi(remotePort)
		if err != nil {
			log.Printf("Invalid remote port: %s", remotePort)
			continue
		}

		lis, err := net.Listen("tcp", fmt.Sprintf("%s:%s", host, localPort))
		if err != nil {
			log.Printf("Error listening: %v", err)
			continue
		}
		if localPort == "0" {
			log.Printf("Forwarding %s to port %s", lis.Addr().String(), remotePort)
		}
		context.AfterFunc(cmd.Context(), func() {
			lis.Close()
		})

		go client.PortForward(cmd.Context(), lis, remotePortInt)
	}
}
