//go:build !clionly && linux

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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
	_ "golang.org/x/crypto/x509roots/fallback"
	"golang.org/x/sys/unix"
)

var importContainerCmd = &cobra.Command{
	Use:   "import [flags] <image-ref>",
	Short: "Import current instance root from a container image",
	Long: `Import a public container image into the current instance root filesystem.

The command remounts '/' as read-write, pulls the image directly from the registry,
and unpacks its merged filesystem into '/'.

No Docker/container engine is used.

Examples:
  velda instance import ubuntu:24.04
  velda instance import ghcr.io/org/project:latest
  velda instance import --auth docker registry.example.com/private/base:dev`,
	Args:   cobra.ExactArgs(1),
	RunE:   runImportContainer,
	Hidden: true,
}

func init() {
	instanceCmd.AddCommand(importContainerCmd)
	f := importContainerCmd.Flags()
	f.String("auth", "anonymous",
		"Registry auth method: anonymous (default), docker (default keychain), google (GCR/AR), manual (stdin prompt)")
	f.Bool("post-install", true, "Whether to run post-install script included in the image, if any. By default, the post-install script will be run if detected. Set this flag to false to skip running the post-install script.")
}

func runImportContainer(cmd *cobra.Command, args []string) error {
	if os.Geteuid() != 0 {
		reexecArgs := append([]string{"sudo", "-E"}, os.Args...)
		return syscall.Exec("/usr/bin/sudo", reexecArgs, os.Environ())
	}

	imageRef := args[0]
	authMethod, _ := cmd.Flags().GetString("auth")
	remoteOpt, err := resolveRegistryAuth(authMethod)
	if err != nil {
		return fmt.Errorf("resolving registry auth: %w", err)
	}

	parsedRef, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", imageRef, err)
	}

	cmd.PrintErrf("Pulling image %s...\n", parsedRef)
	img, err := remote.Image(parsedRef, remoteOpt, remote.WithPlatform(
		v1.Platform{
			Architecture: runtime.GOARCH,
			OS:           runtime.GOOS,
		},
	))
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", parsedRef, err)
	}

	if err := remountRootReadWrite(); err != nil {
		return fmt.Errorf("remounting root read-write: %w", err)
	}

	cmd.PrintErrln("Unpacking image filesystem to / ...")
	reader := mutate.Extract(img)
	defer reader.Close()

	if err := extractTarToRoot(cmd, reader); err != nil {
		return fmt.Errorf("extracting image to /: %w", err)
	}

	cmd.PrintErrf("Successfully imported %s\n", parsedRef)
	if postInstall, _ := cmd.Flags().GetBool("post-install"); postInstall {
		cmd.PrintErrln("Running post-install script to initialize as Velda sandbox...")
		initScript := getInitSandboxScript()
		shell := exec.Command("/bin/sh")
		shell.Stdin = strings.NewReader(initScript)
		shell.Stdout = os.Stdout
		shell.Stderr = os.Stderr
		if err := shell.Run(); err != nil {
			return fmt.Errorf("running post-install script: %w", err)
		}
	}
	return nil
}

func remountRootReadWrite() error {
	if err := unix.Mount("", "/", "", unix.MS_REMOUNT, "rw"); err == nil {
		return nil
	}
	if err := unix.Mount("/", "/", "", unix.MS_REMOUNT, "rw"); err == nil {
		return nil
	}
	cmd := exec.Command("mount", "-o", "remount,rw", "/")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount -o remount,rw / failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func extractTarToRoot(cmd *cobra.Command, r io.Reader) error {
	tr := tar.NewReader(r)

	// This is only used to initialize from an empty root, so we don't check if writing to symlink will escape.
	var entryCount int64
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar stream: %w", err)
		}

		dstPath, err := cleanExtractPath(hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dstPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("creating dir %s: %w", dstPath, err)
			}
			if err := os.Chown(dstPath, hdr.Uid, hdr.Gid); err != nil {
				return fmt.Errorf("chown dir %s: %w", dstPath, err)
			}
			if err := os.Chmod(dstPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("chmod dir %s: %w", dstPath, err)
			}
			if err := os.Chtimes(dstPath, safeTime(hdr.AccessTime), safeTime(hdr.ModTime)); err != nil {
				return fmt.Errorf("chtimes dir %s: %w", dstPath, err)
			}

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
				return fmt.Errorf("ensuring parent dir for symlink %s: %w", dstPath, err)
			}
			_ = os.RemoveAll(dstPath)
			if err := os.Symlink(hdr.Linkname, dstPath); err != nil {
				return fmt.Errorf("creating symlink %s -> %s: %w", dstPath, hdr.Linkname, err)
			}
			if err := os.Lchown(dstPath, hdr.Uid, hdr.Gid); err != nil {
				return fmt.Errorf("lchown symlink %s: %w", dstPath, err)
			}

		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
				return fmt.Errorf("ensuring parent dir for hard link %s: %w", dstPath, err)
			}
			linkTarget, err := cleanExtractPath(hdr.Linkname)
			if err != nil {
				return fmt.Errorf("invalid hard-link target %q: %w", hdr.Linkname, err)
			}
			_ = os.RemoveAll(dstPath)
			if err := os.Link(linkTarget, dstPath); err != nil {
				return fmt.Errorf("creating hard link %s -> %s: %w", dstPath, linkTarget, err)
			}

		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
				return fmt.Errorf("ensuring parent dir for file %s: %w", dstPath, err)
			}
			file, err := os.OpenFile(dstPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("creating file %s: %w", dstPath, err)
			}
			if total, err := io.Copy(file, tr); err != nil {
				file.Close()
				return fmt.Errorf("writing file %s: %w", dstPath, err)
			} else {
				written += total
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("closing file %s: %w", dstPath, err)
			}
			if err := os.Chown(dstPath, hdr.Uid, hdr.Gid); err != nil {
				return fmt.Errorf("chown file %s: %w", dstPath, err)
			}
			if err := os.Chmod(dstPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("chmod file %s: %w", dstPath, err)
			}
			if err := os.Chtimes(dstPath, safeTime(hdr.AccessTime), safeTime(hdr.ModTime)); err != nil {
				return fmt.Errorf("chtimes file %s: %w", dstPath, err)
			}

		default:
			continue
		}

		entryCount++
		if entryCount%1000 == 0 {
			cmd.PrintErrf("Extracted %d entries, total %s written...\n", entryCount, formatSize(written))
		}
	}

	// Ensure the command prints a completion line even for small images.
	cmd.PrintErrf("Extracted %d entries, total %s written...\n", entryCount, formatSize(written))
	return nil
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func cleanExtractPath(name string) (string, error) {
	trimmed := strings.TrimPrefix(name, "/")
	if trimmed == "" || trimmed == "." {
		return "/", nil
	}
	cleaned := filepath.Clean("/" + trimmed)
	if !strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("invalid tar path %q", name)
	}
	if strings.HasPrefix(cleaned, "/../") || cleaned == "/.." {
		return "", fmt.Errorf("tar entry escapes root: %q", name)
	}
	return cleaned, nil
}

func safeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0)
	}
	return t
}
