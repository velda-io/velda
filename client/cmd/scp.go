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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

// Define a struct to encapsulate boolean options
type CopyOptions struct {
	Recursive     bool
	Verbose       bool
	PreserveMode  bool
	PreserveOwner bool
	PreserveTimes bool
}

// scpCmd represents the scp command
var scpCmd = &cobra.Command{
	Use:     "scp SOURCE... DESTINATION",
	Example: "velda scp local_file.txt instance:/path/to/destination/\nvelda scp instance:/path/to/file.txt local_destination/",
	Short:   "Copy files between local and remote machines",
	Args:    cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()

		var instance string
		for _, arg := range args {
			if strings.Contains(arg, ":") {
				instanceCur := strings.Split(arg, ":")[0]
				if instance != "" && instance != instanceCur {
					return fmt.Errorf("Multiple instances specified: %s and %s", instance, instanceCur)
				}
				instance = instanceCur
			}
		}

		DebugLog("Using instance %s", instance)
		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(), instance, !clientlib.FallbackToSession)

		if err != nil {
			return err
		}

		brokerClient, err := clientlib.GetBrokerClient()
		if err != nil {
			return err
		}

		user, _ := cmd.Flags().GetString("user")
		resp, err := brokerClient.RequestSession(cmd.Context(), &proto.SessionRequest{
			ServiceName: cmd.Flag("service-name").Value.String(),
			InstanceId:  instanceId,
			Pool:        cmd.Flag("pool").Value.String(),
			User:        user,
		})
		if err != nil {
			return err
		}

		client, err := clientlib.SshConnect(cmd, resp.GetSshConnection(), user)
		if err != nil {
			return fmt.Errorf("Error connecting to SSH: %v", err)
		}
		defer client.Close()

		// Create a new SFTP client
		sftpClient, err := sftp.NewClient(client.Client)
		if err != nil {
			return fmt.Errorf("Error creating SFTP client: %v", err)
		}
		defer sftpClient.Close()

		// The last argument is the destination
		dest := args[len(args)-1]
		sources := args[:len(args)-1]

		options := CopyOptions{
			Recursive:     cmd.Flags().Lookup("recursive").Value.String() == "true",
			Verbose:       cmd.Flags().Lookup("verbose").Value.String() == "true",
			PreserveMode:  cmd.Flags().Lookup("preserve-mode").Value.String() == "true",
			PreserveOwner: cmd.Flags().Lookup("preserve-owner").Value.String() == "true",
			PreserveTimes: cmd.Flags().Lookup("preserve-times").Value.String() == "true",
		}

		// Check if destination is remote (contains ":")
		isDestRemote := strings.Contains(dest, ":")

		if isDestRemote {
			// Local to remote copy
			remoteDest := strings.Split(dest, ":")[1]
			return copyLocalToRemote(cmd.Context(), sftpClient, sources, remoteDest, options)
		} else {
			// Remote to local copy
			return copyRemoteToLocal(cmd.Context(), sftpClient, sources, dest, options)
		}
	},
}

func copyLocalToRemote(ctx context.Context, sftpClient *sftp.Client, sources []string, remoteDest string, options CopyOptions) error {
	if remoteDest == "" {
		remoteDest = "."
	}
	for _, source := range sources {
		if strings.Contains(source, ":") {
			return fmt.Errorf("cannot copy from remote to remote")
		}

		info, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("Error stat'ing source %s: %v", source, err)
		}

		if info.IsDir() {
			if !options.Recursive {
				return fmt.Errorf("source is a directory, use -r to copy recursively")
			}
			err = copyDirToRemote(ctx, sftpClient, source, remoteDest, options)
			if err != nil {
				return err
			}
		} else {
			err = copyFileToRemote(ctx, sftpClient, source, remoteDest, options)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFileToRemote(ctx context.Context, sftpClient *sftp.Client, localPath string, remoteDest string, options CopyOptions) error {
	// Open the local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("Error opening local file %s: %v", localPath, err)
	}
	defer localFile.Close()

	// Get file info for size and permissions
	localInfo, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("Error getting file info for %s: %v", localPath, err)
	}

	// Determine the remote file path
	remoteFilePath := remoteDest
	if stat, err := sftpClient.Stat(remoteDest); err == nil && stat.IsDir() {
		remoteFilePath = filepath.Join(remoteDest, filepath.Base(localPath))
	}

	// Create or truncate the remote file
	remoteFile, err := sftpClient.Create(remoteFilePath)
	if err != nil {
		return fmt.Errorf("Error creating remote file %s: %v", remoteFilePath, err)
	}
	defer remoteFile.Close()

	// Copy the file content
	if _, err = remoteFile.ReadFrom(localFile); err != nil {
		return fmt.Errorf("Error copying file content for %s: %v", localPath, err)
	}

	// Set permissions if preserveMode flag is set
	if options.PreserveMode {
		if err = sftpClient.Chmod(remoteFilePath, localInfo.Mode()); err != nil {
			return fmt.Errorf("Error setting permissions for %s: %v", remoteFilePath, err)
		}
	}

	// Set ownership if preserveOwner flag is set
	if options.PreserveOwner {
		if stat, ok := localInfo.Sys().(*syscall.Stat_t); ok {
			if err = sftpClient.Chown(remoteFilePath, int(stat.Uid), int(stat.Gid)); err != nil {
				return fmt.Errorf("Error setting ownership for %s: %v", remoteFilePath, err)
			}
		}
	}

	// Set timestamps if preserveTimes flag is set
	if options.PreserveTimes {
		if stat, ok := localInfo.Sys().(*syscall.Stat_t); ok {
			atime := stat.Atim
			mtime := stat.Mtim
			if err = sftpClient.Chtimes(remoteFilePath, time.Unix(atime.Sec, atime.Nsec), time.Unix(mtime.Sec, mtime.Nsec)); err != nil {
				return fmt.Errorf("Error setting timestamps for %s: %v", remoteFilePath, err)
			}
		}
	}

	if options.Verbose {
		fmt.Printf("'%s' -> '%s'\n", localPath, remoteFilePath)
	}

	return nil
}

func copyDirToRemote(ctx context.Context, sftpClient *sftp.Client, localDir string, remoteDest string, options CopyOptions) error {
	// Check if the remote destination exists
	remoteDestInfo, err := sftpClient.Stat(remoteDest)
	if err != nil {
		// If the remote destination does not exist, use it directly as the target
		if err := sftpClient.MkdirAll(remoteDest); err != nil {
			return fmt.Errorf("Error creating remote directory %s: %v", remoteDest, err)
		}
	} else if !remoteDestInfo.IsDir() {
		return fmt.Errorf("remote destination %s is not a directory", remoteDest)
	} else {
		// If the remote destination exists and is a directory, append the local directory name
		remoteDest = filepath.Join(remoteDest, filepath.Base(localDir))
		if err := sftpClient.MkdirAll(remoteDest); err != nil {
			return fmt.Errorf("Error creating remote directory %s: %v", remoteDest, err)
		}
	}

	// Walk through local directory and copy files
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if path == localDir {
			return nil
		}

		// Get the relative path from the source directory
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return fmt.Errorf("Error getting relative path: %v", err)
		}

		// Construct the destination path on the remote
		remoteDestPath := filepath.Join(remoteDest, relPath)

		if info.IsDir() {
			// Create directory on the remote
			if err := sftpClient.MkdirAll(remoteDestPath); err != nil {
				return fmt.Errorf("Error creating remote directory %s: %v", remoteDestPath, err)
			}
			if options.Verbose {
				fmt.Printf("'%s' -> '%s'\n", path, remoteDestPath)
			}
			// Set permissions if preserveMode flag is set
			if options.PreserveMode {
				if err = sftpClient.Chmod(remoteDestPath, info.Mode()); err != nil {
					return fmt.Errorf("Error setting permissions for %s: %v", remoteDestPath, err)
				}
			}
			// TODO: Implement logic for preserveOwner and preserveTimes if needed
		} else {
			// Copy file to remote
			return copyFileToRemote(ctx, sftpClient, path, remoteDestPath, options)
		}

		return nil
	})
}

func copyRemoteToLocal(ctx context.Context, sftpClient *sftp.Client, sources []string, localDest string, options CopyOptions) error {
	for _, source := range sources {
		if !strings.Contains(source, ":") {
			return fmt.Errorf("source must be remote for remote to local copy")
		}

		remotePath := strings.Split(source, ":")[1]
		remoteInfo, err := sftpClient.Stat(remotePath)
		if err != nil {
			return fmt.Errorf("Error stat'ing remote path %s: %v", remotePath, err)
		}

		if remoteInfo.IsDir() {
			if !options.Recursive {
				return fmt.Errorf("source is a directory, use -r to copy recursively")
			}
			var localPath string
			if len(sources) > 1 {
				localPath = filepath.Join(localDest, filepath.Base(remotePath))
			} else if _, err := os.Stat(localDest); errors.Is(err, os.ErrNotExist) {
				localPath = localDest
			} else {
				localPath = filepath.Join(localDest, filepath.Base(remotePath))
			}
			err = copyDirToLocal(ctx, sftpClient, remotePath, localPath, options)
			if err != nil {
				return err
			}
		} else {
			err = copyFileToLocal(ctx, sftpClient, remotePath, localDest, options)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFileToLocal(ctx context.Context, sftpClient *sftp.Client, remotePath string, localDest string, options CopyOptions) error {
	// Open the remote file
	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("Error opening remote file %s: %v", remotePath, err)
	}
	defer remoteFile.Close()

	// Get file info for size and permissions
	remoteInfo, err := remoteFile.Stat()
	if err != nil {
		return fmt.Errorf("Error getting file info for %s: %v", remotePath, err)
	}

	// Determine the local file path
	localFilePath := localDest
	if info, err := os.Stat(localDest); err == nil && info.IsDir() {
		localFilePath = filepath.Join(localDest, filepath.Base(remotePath))
	}

	// Create or truncate the local file
	localFile, err := os.Create(localFilePath)
	if err != nil {
		return fmt.Errorf("Error creating local file %s: %v", localFilePath, err)
	}
	defer localFile.Close()

	// Copy the file content
	if _, err = localFile.ReadFrom(remoteFile); err != nil {
		return fmt.Errorf("Error copying file content for %s: %v", remotePath, err)
	}

	// Set permissions if preserveMode flag is set
	if options.PreserveMode {
		if err = localFile.Chmod(remoteInfo.Mode()); err != nil {
			return fmt.Errorf("Error setting permissions for %s: %v", localFilePath, err)
		}
	}

	// Set ownership if preserveOwner flag is set
	if options.PreserveOwner {
		if stat, ok := remoteInfo.Sys().(*syscall.Stat_t); ok {
			if err = os.Chown(localFilePath, int(stat.Uid), int(stat.Gid)); err != nil {
				return fmt.Errorf("Error setting ownership for %s: %v", localFilePath, err)
			}
		}
	}

	// Set timestamps if preserveTimes flag is set
	if options.PreserveTimes {
		if stat, ok := remoteInfo.Sys().(*syscall.Stat_t); ok {
			atime := stat.Atim
			mtime := stat.Mtim
			if err = os.Chtimes(localFilePath, time.Unix(atime.Sec, atime.Nsec), time.Unix(mtime.Sec, mtime.Nsec)); err != nil {
				return fmt.Errorf("Error setting timestamps for %s: %v", localFilePath, err)
			}
		}
	}

	if options.Verbose {
		fmt.Printf("'%s' -> '%s'\n", remotePath, localFilePath)
	}

	return nil
}

func copyDirToLocal(ctx context.Context, sftpClient *sftp.Client, remoteDir string, localDest string, options CopyOptions) error {
	// Make sure local destination exists as a directory
	localDestInfo, err := os.Stat(localDest)
	if err != nil {
		// Create the directory if it doesn't exist
		if err := os.MkdirAll(localDest, 0755); err != nil {
			return fmt.Errorf("Error creating local directory %s: %v", localDest, err)
		}
	} else if !localDestInfo.IsDir() {
		return fmt.Errorf("local destination %s is not a directory", localDest)
	}

	// List all files in the remote directory
	files, err := sftpClient.ReadDir(remoteDir)
	if err != nil {
		return fmt.Errorf("Error reading remote directory %s: %v", remoteDir, err)
	}

	if options.Verbose {
		fmt.Printf("'%s' -> '%s'\n", remoteDir, localDest)
	}

	// Copy each file and directory
	for _, file := range files {
		remotePath := filepath.Join(remoteDir, file.Name())
		localPath := filepath.Join(localDest, file.Name())

		if file.IsDir() {
			// Recursively copy directory
			if err := copyDirToLocal(ctx, sftpClient, remotePath, localPath, options); err != nil {
				return err
			}
		} else {
			// Copy file
			if err := copyFileToLocal(ctx, sftpClient, remotePath, localPath, options); err != nil {
				return err
			}
		}
	}

	return nil
}

func init() {
	rootCmd.AddCommand(scpCmd)
	scpCmd.Flags().StringP("service-name", "s", "", "Service name, which can be used to identify session later.")
	scpCmd.Flags().String("pool", "shell", "Pool name to create session if session does not exist.")
	scpCmd.Flags().StringP("user", "u", "user", "User to login as")
	scpCmd.Flags().BoolP("recursive", "r", false, "Recursively copy entire directories")
	scpCmd.Flags().BoolP("verbose", "v", false, "Verbose mode: print detailed messages")
	scpCmd.Flags().Bool("preserve-mode", false, "Preserve file mode (permissions)")
	scpCmd.Flags().Bool("preserve-owner", false, "Preserve file owner")
	scpCmd.Flags().Bool("preserve-times", false, "Preserve file modification and access times")

	// Add a short alias for the preserve flag
	scpCmd.Flags().BoolP("preserve", "p", false, "Preserve file mode, owner, and times")

	// Bind the preserve flag to enable all sub-flags
	scpCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		preserve, _ := cmd.Flags().GetBool("preserve")
		if preserve {
			cmd.Flags().Set("preserve-mode", "true")
			cmd.Flags().Set("preserve-owner", "true")
			cmd.Flags().Set("preserve-times", "true")
		}
		return nil
	}
}
