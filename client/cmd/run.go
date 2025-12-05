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
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
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
	// Parse tags flag in format key=value,key2=value2 and attach to request
	tagsStr, _ := cmd.Flags().GetString("tags")
	if tagsStr != "" {
		tags := make(map[string]string)
		pairs := strings.Split(tagsStr, ",")
		for _, p := range pairs {
			if p == "" {
				continue
			}
			parts := strings.SplitN(p, "=", 2)
			if len(parts) == 1 {
				// key without value, set to empty string
				tags[parts[0]] = ""
			} else {
				tags[parts[0]] = parts[1]
			}
		}
		sessionReq.Tags = tags
	}
	checkPoint, _ := cmd.Flags().GetBool("checkpoint-on-idle")
	keepAlive, _ := cmd.Flags().GetBool("keep-alive")
	keepAliveTime, _ := cmd.Flags().GetDuration("keep-alive-time")
	priority, _ := cmd.Flags().GetInt64("priority")
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
	sessionReq.Priority = priority

	batch, _ := cmd.Flags().GetBool("batch")
	if batch {
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
	quiet, _ := cmd.Flags().GetBool("quiet")

	if !quiet && !batch {
		cmd.PrintErrf("Requesting compute node from pool %s\n", sessionReq.Pool)
	}
	DebugLog("Sending session request: %v", sessionReq)

	resp, err := brokerClient.RequestSession(cmd.Context(), sessionReq)
	if err != nil {
		return err
	}
	if !quiet && !batch {
		cmd.PrintErrln("Node allocated, connecting...")
	}
	DebugLog("Got response: %s. Connecting ", resp.String())
	if batch {
		taskId := resp.GetTaskId()
		fmt.Printf("%s\n", taskId)

		followFlag, _ := cmd.Flags().GetBool("follow")
		if followFlag {
			// follow mode: extracted to helper
			if err := followTask(taskId); err != nil {
				return err
			}
		}

		if !quiet && clientlib.GetAgentConfig().GetTaskId() == "" && clientlib.GetAgentConfig().GetBroker().GetPublicAddress() != "" {
			publicAddr := clientlib.GetAgentConfig().GetBroker().GetPublicAddress()
			taskUrl := fmt.Sprintf("%s/tasks/%s", publicAddr, taskId)
			cmd.PrintErrf("Track task at %s\n", taskUrl)
		}
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
	tty := defaultShell
	switch ttymode {
	case "auto":
		tty = isatty.IsTerminal(os.Stdin.Fd())
	case "yes", "true", "y":
		tty = true
	case "no", "false", "n":
		tty = false
	default:
		return fmt.Errorf("Invalid tty mode: %s", ttymode)
	}
	noinput, _ := cmd.Flags().GetBool("noinput")
	interactive := !noinput || tty || defaultShell

	if interactive {
		session.Stdin = os.Stdin
	}
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	if !quiet {
		cmd.PrintErrln("Start the workload.")
	}
	if tty {
		if err := session.RequestPty(true); err != nil {
			return fmt.Errorf("Error requesting pty: %v", err)
		}
		defer session.RestoreTty()
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
			if tty {
				session.RestoreTty()
			}
			var status struct {
				Status uint32
			}
			if err := ssh.Unmarshal(req.Payload, &status); err != nil {
				*returnCode = -1
				return fmt.Errorf("Error unmarshalling exit-status: %v", err)
			}
			*returnCode = int(status.Status)
			if !quiet {
				cmd.PrintErrf("Workload exited with status %d\n", status.Status)
			}
			return nil
		case "exit-signal":
			if tty {
				session.RestoreTty()
			}
			signalInfo := struct {
				Signal string
				Core   bool
				Msg    string
				Lang   string
			}{}
			if err := ssh.Unmarshal(req.Payload, &signalInfo); err != nil {
				*returnCode = -1
				return fmt.Errorf("Error unmarshalling exit-signal: %v", err)
			}
			if !quiet {
				cmd.PrintErrf("Workload exited with signal %s\n", signalInfo.Signal)
			}
			*returnCode = -1
			return nil
		}
	}
	return nil
}

func getWorkload(cmd *cobra.Command, args []string) (*proto.Workload, error) {
	if clientlib.IsInSession() {
		cwd, _ := cmd.Flags().GetString("directory")
		if cwd == "" && clientlib.IsInSession() {
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

		// Determine environment handling based on context
		var environs []string

		shards, _ := cmd.Flags().GetInt32("total-shards")
		gang, _ := cmd.Flags().GetBool("gang")
		shardMode := proto.Workload_SHARD_SCHEDULING_UNSPECIFIED
		if gang {
			shardMode = proto.Workload_SHARD_SCHEDULING_GANG
		}
		// In session or non-batch: use current environment
		environs = os.Environ()
		environs = append(environs, clientlib.RemoveLocalEnvs()...)
		commandPath, err := exec.LookPath(command)
		if err != nil {
			return nil, fmt.Errorf("Error finding command path: %v", err)
		}
		return &proto.Workload{
			Command:         command,
			CommandPath:     commandPath,
			Args:            argv,
			WorkingDir:      cwd,
			Environs:        environs,
			Shell:           shell,
			Uid:             uint32(uid),
			Gid:             uint32(gid),
			Groups:          groupsUint32,
			TotalShards:     shards,
			ShardScheduling: shardMode,
		}, nil
	}
	workingDir := cmd.Flag("directory").Value.String()

	envFlags, _ := cmd.Flags().GetStringSlice("env")
	environs := make([]string, 0, len(envFlags))

	for _, envSpec := range envFlags {
		parts := strings.SplitN(envSpec, "=", 2)
		if len(parts) == 1 {
			// KEY form - use current system value
			if value, ok := os.LookupEnv(parts[0]); ok {
				environs = append(environs, fmt.Sprintf("%s=%s", parts[0], value))
			}
		} else {
			// KEY=VALUE form
			environs = append(environs, envSpec)
		}
	}
	loginUser := cmd.Flag("user").Value.String()
	return &proto.Workload{
		Command:    args[0],
		Args:       args[1:],
		WorkingDir: workingDir,
		Environs:   environs,
		LoginUser:  loginUser,
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

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringP("pool", "P", "shell", "Pool to run the workload in")
	runCmd.Flags().String("instance", "", "Instance name or ID. Default to the current instance if running in Velda, or default-instance config.")
	runCmd.Flags().String("tty", "auto", "TTy mode. auto|yes|no. Default to auto, will be based on input.")
	runCmd.Flags().BoolP("noinput", "I", false, "Don't take input from stdin. Only used with a command.")
	runCmd.Flags().Bool("shell", false, "Use shell")
	runCmd.Flags().Bool("batch", false, "Batch mode. Queue the job, and return immediately")
	runCmd.Flags().Bool("new-session", false, "Always create a new session")
	runCmd.Flags().Bool("keep-alive", false, "Keep the session alive after all processes exits even if all connections are closed.")
	runCmd.Flags().BoolP("quiet", "q", false, "Suppress all non-error loggings.")
	runCmd.Flags().Bool("checkpoint-on-idle", false, "If all connections are closed, check-point the session. Imply keep-alive.")
	runCmd.Flags().StringP("service-name", "s", "", "Service name, which can be used to identify session later. Default is ssh if connected externally, or empty.")
	runCmd.Flags().BoolP("follow", "f", false, "Follow the execution of the command. Only valid for batch mode.")
	runCmd.Flags().String("session-id", "", "Reconnect to specific session. If not provided, it will try to find a session with the service name or create a new session.")
	runCmd.Flags().StringP("directory", "C", "", "Working directory. Default to current directory if running in Velda and a command is provided, otherwise home directory.")
	runCmd.Flags().StringP("user", "u", "user", "User to run the command as. Ignored if running in a Velda session with a command provided, where it will always use the current user.")
	runCmd.Flags().StringP("name", "n", "", "Name of the the task")
	runCmd.Flags().StringArrayP("publish", "p", nil, "Publish a session's port to the current session. Format: [host:]local_port:session_port")
	runCmd.Flags().StringSlice("after", nil, "For batch task, run it after other tasks are finished(either succeeded or failed)")
	runCmd.Flags().StringSlice("after-success", nil, "For batch task, run it after other tasks finished successfully (return 0)")
	runCmd.Flags().StringSlice("after-fail", nil, "For batch task, run it after other tasks are failed")
	runCmd.Flags().StringSliceP("labels", "l", nil, "Labels for the task")
	runCmd.Flags().String("tags", "", "Tags to set on the session in format key=value,key2=value2. Set value empty to remove tag.")
	runCmd.Flags().Int32P("total-shards", "N", 0, "Total number of shards to run. Each shard will have environment variable VELDA_SHARD_ID & VELDA_TOTAL_SHARDS set. Batch job only.")
	runCmd.Flags().Int64("priority", 0, "Priority of the task. Lower number means higher priority.")
	runCmd.Flags().Bool("gang", false, "Enable gang scheduling for the task. Ignored if total shard is 0")
	runCmd.Flags().Duration("keep-alive-time", 0, "How long to keep the session alive after all connections are closed. Default to 0, which means no keep-alive.")
	runCmd.Flags().StringSliceP("env", "e", nil, "Environment variables to pass to the batch job in KEY=VALUE or KEY form. If VALUE is omitted, use current system value. Only valid for batch mode outside of session.")
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

// followTask will stream task status and logs until completion or cancellation.
// On first Ctrl-C it prompts the user to cancel the task; if the user declines,
// observation continues; a second Ctrl-C will exit immediately.
func followTask(taskId string) error {
	conn, err := clientlib.GetApiConnection()
	if err != nil {
		return fmt.Errorf("Error getting API connection for follow: %w", err)
	}
	defer conn.Close()
	taskClient := proto.NewTaskServiceClient(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	statusCh := make(chan *proto.Task)
	errorCh := make(chan error, 2)

	// watch status
	go func() {
		stream, err := taskClient.WatchTask(ctx, &proto.GetTaskRequest{TaskId: taskId})
		if err != nil {
			errorCh <- fmt.Errorf("Error opening watch stream: %w", err)
			return
		}
		for {
			task, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					close(statusCh)
					return
				}
				errorCh <- fmt.Errorf("Error receiving status update: %w", err)
				return
			}
			statusCh <- task
			status := task.GetStatus()
			if status == proto.TaskStatus_TASK_STATUS_SUCCESS || status == proto.TaskStatus_TASK_STATUS_FAILURE || status == proto.TaskStatus_TASK_STATUS_FAILED_UPSTREAM || status == proto.TaskStatus_TASK_STATUS_CANCELLED {
				// terminal state: close status channel and exit
				close(statusCh)
				return
			}
		}
	}()

	// logs will be started once the task is running

	// handle Ctrl-C: detach first (stop streaming), then prompt to cancel
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Reset(os.Interrupt)

	var lastState proto.TaskStatus = proto.TaskStatus_TASK_STATUS_UNSPECIFIED
	logsStarted := false
	for {
		select {
		case task, ok := <-statusCh:
			if !ok {
				return nil
			}
			state := task.GetStatus()
			if state != lastState {
				lastState = state
				fmt.Fprintf(os.Stderr, "Task %s status: %s\n", taskId, state.String())
				// start logs when task transitions to RUNNING-like state
				if !logsStarted && (state == proto.TaskStatus_TASK_STATUS_RUNNING) {
					logsStarted = true
					go streamTaskLogs(ctx, taskClient, taskId, errorCh)
				}
			}
		case err := <-errorCh:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-sigCh:
			// Detach immediately by cancelling the context to stop streaming goroutines.
			cancel()
			// Prompt the user whether they want to cancel the job. Regardless of answer,
			// we return (detached).
			fmt.Fprint(os.Stderr, "\nDetached. Cancel the task? [y/N]: ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if strings.EqualFold(line, "y") || strings.EqualFold(line, "yes") {
				if _, err := taskClient.CancelJob(context.Background(), &proto.CancelJobRequest{JobId: taskId}); err != nil {
					fmt.Fprintf(os.Stderr, "Error cancelling job: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "Job %s cancelled\n", taskId)
				}
			}
			return nil
		}
	}
}

// streamTaskLogs streams logs for the given taskId and reports errors on errorCh.
func streamTaskLogs(ctx context.Context, taskClient proto.TaskServiceClient, taskId string, errorCh chan<- error) {
	stream, err := taskClient.Logs(ctx, &proto.LogTaskRequest{TaskId: taskId, Follow: true})
	if err != nil {
		errorCh <- fmt.Errorf("Error opening log stream: %w", err)
		return
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return
			}
			errorCh <- fmt.Errorf("Error receiving log data: %w", err)
			return
		}
		data := resp.GetData()
		outputStream := os.Stdout
		if resp.GetStream() == proto.LogTaskResponse_STREAM_STDERR {
			outputStream = os.Stderr
		}
		if _, err := outputStream.Write(data); err != nil {
			errorCh <- err
			return
		}
	}
}
