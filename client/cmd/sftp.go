// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//go:build linux
// +build linux

package cmd

import (
	"io"
	"log"
	_ "net/http/pprof"
	"os"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
)

type readWriteCloser struct {
	io.Reader
	io.Writer
}

func (rwc *readWriteCloser) Close() error {
	return nil
}

// sftpCmd represents the sftp command
var sftpCmd = &cobra.Command{
	Use:   "sftp",
	Short: "Start an SFTP server through stdin/stdout.",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Inline SFTP server logic
		wrapped := &readWriteCloser{Reader: os.Stdin, Writer: os.Stdout}

		server, err := sftp.NewServer(wrapped)
		if err != nil {
			return err
		}

		log.Println("Starting SFTP server...")
		if err := server.Serve(); err != nil {
			return err
		}

		return nil
	},
}

func init() {
	AgentCmd.AddCommand(sftpCmd)
}
