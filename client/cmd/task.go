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
	"fmt"
	"io"
	"os"

	goproto "google.golang.org/protobuf/proto"

	"github.com/spf13/cobra"
	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/utils"
)

const defaultTaskColumns = "id,pool,status,Start time=startedAt"

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage tasks",
	Args:  cobra.NoArgs,
}

// output helpers are provided by package utils

var getTaskCmd = &cobra.Command{
	Use:   "get <task-id>",
	Short: "Get task details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		defer conn.Close()
		client := proto.NewTaskServiceClient(conn)

		taskId := args[0]
		task, err := client.GetTask(cmd.Context(), &proto.GetTaskRequest{TaskId: taskId})
		if err != nil {
			return fmt.Errorf("Error getting task: %w", err)
		}

		outFmt, _ := cmd.Flags().GetString("output")
		if outFmt == "" {
			outFmt = defaultTaskColumns
		}
		header, _ := cmd.Flags().GetBool("header")

		// Use shared output util. Provide full message so json/yaml will print the full Task.
		return utils.PrintListOutput(task, []goproto.Message{task}, header, outFmt, os.Stdout)
	},
}

var listTaskCmd = &cobra.Command{
	Use:   "list [parent-task-id]",
	Short: "List sub-tasks of a task (or top-level tasks if no parent provided)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		defer conn.Close()
		client := proto.NewTaskServiceClient(conn)

		parentId := ""
		if len(args) == 1 {
			parentId = args[0]
		}

		pageToken, _ := cmd.Flags().GetString("page-token")
		maxResults, _ := cmd.Flags().GetInt32("max-results")
		fetchAll, _ := cmd.Flags().GetBool("all")
		header, _ := cmd.Flags().GetBool("header")

		outFmt, _ := cmd.Flags().GetString("output")
		if outFmt == "" {
			outFmt = defaultTaskColumns
		}
		for {
			resp, err := client.ListTasks(cmd.Context(), &proto.ListTasksRequest{
				ParentId:  parentId,
				PageToken: pageToken,
				PageSize:  maxResults,
			})
			if err != nil {
				return fmt.Errorf("Error listing tasks: %w", err)
			}
			if err := utils.PrintListOutput(resp, taskToMessageList(resp.GetTasks()), header, outFmt, os.Stdout); err != nil {
				return fmt.Errorf("failed to print: %w", err)
			}
			if resp.GetNextPageToken() != "" && !fetchAll {
				fmt.Printf("Next page token: %s\n", resp.GetNextPageToken())
			}
			pageToken = resp.GetNextPageToken()
			if pageToken == "" || !fetchAll {
				break
			}
		}
		return nil
	},
}

var searchTaskCmd = &cobra.Command{
	Use:   "search",
	Short: "Search tasks by label filters",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		defer conn.Close()
		client := proto.NewTaskServiceClient(conn)

		labelFilters, _ := cmd.Flags().GetStringSlice("label")
		pageToken, _ := cmd.Flags().GetString("page-token")
		maxResults, _ := cmd.Flags().GetInt32("max-results")
		fetchAll, _ := cmd.Flags().GetBool("all")
		header, _ := cmd.Flags().GetBool("header")

		outFmt, _ := cmd.Flags().GetString("output")
		if outFmt == "" {
			outFmt = defaultTaskColumns
		}
		for {
			resp, err := client.SearchTasks(cmd.Context(), &proto.SearchTasksRequest{
				LabelFilters: labelFilters,
				PageToken:    pageToken,
				PageSize:     maxResults,
			})
			if err != nil {
				return fmt.Errorf("Error searching tasks: %w", err)
			}
			if err := utils.PrintListOutput(resp, taskToMessageList(resp.GetTasks()), header, outFmt, os.Stdout); err != nil {
				return fmt.Errorf("failed to print: %w", err)
			}
			if resp.GetNextPageToken() != "" && !fetchAll {
				fmt.Printf("Next page token: %s\n", resp.GetNextPageToken())
			}
			pageToken = resp.GetNextPageToken()
			if pageToken == "" || !fetchAll {
				break
			}
		}
		return nil
	},
}

var logTaskCmd = &cobra.Command{
	Use:   "log <task-id>",
	Short: "Stream task logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		defer conn.Close()
		client := proto.NewTaskServiceClient(conn)

		taskId := args[0]
		stream, err := client.Logs(cmd.Context(), &proto.LogTaskRequest{TaskId: taskId})
		if err != nil {
			return fmt.Errorf("Error opening log stream: %w", err)
		}

		for {
			resp, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("Error receiving log data: %w", err)
			}
			data := resp.GetData()
			outputStream := os.Stdout
			if resp.GetStream() == proto.LogTaskResponse_STREAM_STDERR {
				outputStream = os.Stderr
			}
			if _, err := outputStream.Write(data); err != nil {
				return err
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(taskCmd)
	taskCmd.AddCommand(getTaskCmd)
	taskCmd.AddCommand(logTaskCmd)
	taskCmd.AddCommand(listTaskCmd)
	taskCmd.AddCommand(searchTaskCmd)

	listTaskCmd.Flags().String("page-token", "", "Page token")
	listTaskCmd.Flags().Int32("max-results", 0, "Max results")
	listTaskCmd.Flags().BoolP("all", "a", false, "Fetch all results")
	listTaskCmd.Flags().Bool("header", true, "Show header")
	listTaskCmd.Flags().StringP("output", "o", "", "Output format (json|yaml|[[<fields>=]<path>]*)")

	searchTaskCmd.Flags().StringSliceP("label", "l", nil, "Label filters (repeatable)")
	searchTaskCmd.Flags().String("page-token", "", "Page token")
	searchTaskCmd.Flags().Int32("max-results", 0, "Max results")
	searchTaskCmd.Flags().Bool("header", true, "Show header")
	searchTaskCmd.Flags().StringP("output", "o", "", "Output format (json|yaml|[[<fields>=]<path>]*)")

	getTaskCmd.Flags().StringP("output", "o", "", "Output format (json|yaml|[[<fields>=]<path>]*)")
	getTaskCmd.Flags().Bool("header", true, "Show header")
}

func taskToMessageList(tasks []*proto.Task) []goproto.Message {
	var messages []goproto.Message
	for _, task := range tasks {
		messages = append(messages, task)
	}
	return messages
}
