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
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"velda.io/velda/pkg/clientlib"
)

// streamDockerImageToSftp streams a docker image (via `docker export`) directly
// to the provided SFTP client, writing files into the instance. This avoids
// extracting the entire image to a temp directory.
// containerID: container to export (created by caller).
// verbose: print per-file progress; quiet: suppress status output (but docker
// stderr will still be printed on error).
func streamDockerImageToSftp(cmd *cobra.Command, containerID string, sftpClient *sftp.Client, verbose, quiet bool) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not installed or not in the PATH")
	}
	exportCmd := exec.Command("docker", "export", containerID)
	var exportStderr bytes.Buffer
	if quiet {
		// buffer stderr and print only on error
		exportCmd.Stderr = &exportStderr
	} else {
		// stream docker stderr live when not quiet
		exportCmd.Stderr = cmd.ErrOrStderr()
	}
	pipe, err := exportCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get export pipe: %v", err)
	}
	if err := exportCmd.Start(); err != nil {
		if exportStderr.Len() > 0 {
			cmd.ErrOrStderr().Write(exportStderr.Bytes())
		}
		return fmt.Errorf("failed to start docker export: %v", err)
	}

	// Delegate tar parsing and sftp writes to the generic tar reader helper
	if err := streamTarReaderToSftp(cmd, pipe, sftpClient, verbose, quiet); err != nil {
		exportCmd.Wait()
		if exportStderr.Len() > 0 {
			cmd.ErrOrStderr().Write(exportStderr.Bytes())
		}
		return err
	}

	if err := exportCmd.Wait(); err != nil {
		if exportStderr.Len() > 0 {
			cmd.ErrOrStderr().Write(exportStderr.Bytes())
		}
		return fmt.Errorf("docker export failed: %v", err)
	}

	return nil
}

// streamTarReaderToSftp parses a tar stream from r and writes entries to sftpClient.
// It preserves mode, ownership and times where possible.
func streamTarReaderToSftp(cmd *cobra.Command, r io.Reader, sftpClient *sftp.Client, verbose, quiet bool) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %v", err)
		}

		name := hdr.Name
		if filepath.IsAbs(name) {
			name = name[1:]
		}
		remotePath := filepath.Join("/", filepath.FromSlash(name))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := sftpClient.MkdirAll(remotePath); err != nil {
				return fmt.Errorf("failed to create remote directory %s: %v", remotePath, err)
			}
			if err := sftpClient.Chmod(remotePath, os.FileMode(hdr.Mode)); err != nil {
				if !quiet {
					cmd.Printf("Warning: failed to set mode for %s: %v\n", remotePath, err)
				}
			}
			if err := sftpClient.Chtimes(remotePath, hdr.ModTime, hdr.ModTime); err != nil {
				if !quiet {
					cmd.Printf("Warning: failed to set times for %s: %v\n", remotePath, err)
				}
			}
			if err := sftpClient.Chown(remotePath, hdr.Uid, hdr.Gid); err != nil {
				if !quiet {
					cmd.Printf("Warning: failed to set ownership for %s: %v\n", remotePath, err)
				}
			}
			if verbose && !quiet {
				cmd.Printf("Created directory: %s\n", remotePath)
			}

		case tar.TypeSymlink:
			if err := sftpClient.Symlink(hdr.Linkname, remotePath); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %v", remotePath, hdr.Linkname, err)
			}
			if verbose && !quiet {
				cmd.Printf("Created symlink: %s -> %s\n", remotePath, hdr.Linkname)
			}

		case tar.TypeReg, tar.TypeRegA:
			if err := sftpClient.MkdirAll(filepath.Dir(remotePath)); err != nil {
				return fmt.Errorf("failed to create parent dir for %s: %v", remotePath, err)
			}
			rf, err := sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
			if err != nil {
				return fmt.Errorf("failed to create remote file %s: %v", remotePath, err)
			}
			if _, err := io.CopyN(rf, tr, hdr.Size); err != nil {
				rf.Close()
				return fmt.Errorf("failed to copy file contents for %s: %v", remotePath, err)
			}
			rf.Close()
			if err := sftpClient.Chmod(remotePath, os.FileMode(hdr.Mode)); err != nil {
				if !quiet {
					cmd.Printf("Warning: failed to set mode for %s: %v\n", remotePath, err)
				}
			}
			if err := sftpClient.Chtimes(remotePath, hdr.ModTime, hdr.ModTime); err != nil {
				if !quiet {
					cmd.Printf("Warning: failed to set times for %s: %v\n", remotePath, err)
				}
			}
			if err := sftpClient.Chown(remotePath, hdr.Uid, hdr.Gid); err != nil {
				if !quiet {
					cmd.Printf("Warning: failed to set ownership for %s: %v\n", remotePath, err)
				}
			}
			if verbose && !quiet {
				cmd.Printf("Copied: %s\n", remotePath)
			}

		default:
			if verbose && !quiet {
				cmd.Printf("Skipping unsupported tar entry %s (type %c)\n", hdr.Name, hdr.Typeflag)
			}
		}
	}
	return nil
}

// runInitScript runs the initialization script on an instance using
// an existing SSH connection (so SFTP and run share the same connection).
func runInitScript(cmd *cobra.Command, sshClient *clientlib.SshClient, scriptContent string, quiet bool) error {
	if !quiet {
		cmd.Printf("Installing Velda in the instance...\n")
	}

	// Create a new session on the existing SSH client
	session, reqs, err := sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %v", err)
	}
	defer session.Close()

	// Create a pipe to feed the script to stdin
	pipeReader, pipeWriter := io.Pipe()
	session.Stdin = pipeReader
	session.Stdout = cmd.OutOrStdout()
	session.Stderr = cmd.ErrOrStderr()
	defer pipeReader.Close()

	// Execute sh on the remote side
	if err := session.Exec("sh"); err != nil {
		pipeWriter.Close()
		return fmt.Errorf("failed to execute sh: %v", err)
	}

	// Feed the script content
	go func() {
		defer pipeWriter.Close()
		io.WriteString(pipeWriter, scriptContent)
	}()

	// Wait for exit status
	for req := range reqs {
		if req == nil {
			break
		}
		if req.Type == "exit-status" {
			var status struct{ Status uint32 }
			if err := ssh.Unmarshal(req.Payload, &status); err == nil {
				if status.Status != 0 {
					return fmt.Errorf("init script failed with exit code %d", status.Status)
				}
			}
			return nil
		}
	}
	return nil
}

func getInitSandboxScript() string {
	return `
set -e
# Initialize user
install_sudo() {
    $(which -s apt-get) && apt-get update && apt-get install -y sudo && return 0
    $(which -s yum) && yum install -y sudo && return 0
    $(which -s dnf) && dnf install -y sudo && return 0
	echo "Could not install sudo, unsupported package manager"
}

$(which -s sudo) || install_sudo
useradd user -m -s /bin/bash || true
passwd -d user
mkdir -p /etc/sudoers.d
echo "user ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/user
usermod -aG sudo user || true

ln -sf /run/velda/velda /usr/bin/velda
ln -sf /run/velda/velda /usr/bin/vbatch
ln -sf /run/velda/velda /usr/bin/vrun
ln -sf /run/velda/velda /sbin/mount.host
`
}
