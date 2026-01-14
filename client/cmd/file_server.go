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
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/fileserver"
)

var (
	fsAddr    string
	fsRoot    string
	fsWorkers int
)

var FileServerCmd = &cobra.Command{
	Use:    "fileserver",
	Short:  "Start file server",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		srv := fileserver.NewFileServer(fsRoot, fsWorkers)
		if err := srv.Start(fsAddr); err != nil {
			return fmt.Errorf("failed to start fileserver: %w", err)
		}

		cmd.Printf("File server listening on %s\n", srv.Addr())

		// Wait for termination signal or context cancel
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-ctx.Done():
		case <-sig:
		}

		srv.Stop()
		return nil
	},
}

func init() {
	FileServerCmd.Flags().StringVarP(&fsAddr, "addr", "a", ":0", "Address to listen on")
	FileServerCmd.Flags().StringVarP(&fsRoot, "root", "r", ".", "Root path to serve")
	FileServerCmd.Flags().IntVarP(&fsWorkers, "workers", "w", 4, "Number of worker goroutines")

	rootCmd.AddCommand(FileServerCmd)
}
