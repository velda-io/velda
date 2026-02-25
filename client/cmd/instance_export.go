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
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var exportContainerCmd = &cobra.Command{
	Use:   "export [flags] [-- entrypoint [args...]]",
	Short: "Pack current '/' into a container image",
	Long: `Pack the current root filesystem into a container image.

Paths on different devices/mounts are skipped automatically (e.g. /proc, /sys,
/dev, and any bind-mounts that live on separate block devices).

Environment variables can be specified as:
  NAME        take the value from the current process environment
  NAME=VALUE  use the given value explicitly

The first positional argument is the container entrypoint; all remaining
positional arguments become CMD.  If no positional arguments are given the
value of $SHELL (falling back to /bin/sh) is used as the entrypoint.

The image is built for the current OS/arch (` + "`" + `runtime.GOOS/runtime.GOARCH` + "`" + `) and
runs as the current UID:GID.

Examples:
  # Push directly to a registry
  velda instance export --push registry.example.com/org/repo:latest

  # Carry specific environment variables into the image
  velda instance export -e HOME -e PATH -e MY_VAR=custom \
      --push registry.example.com/org/repo:latest

  # Push with Google GCR authentication
  velda instance export --push gcr.io/my-project/my-image:latest --auth google

  # Push with manual credentials
  velda instance export --push registry.example.com/org/repo:latest --auth manual

  # Use a custom export config to exclude files
  velda instance export --config /etc/velda-export.yaml --push registry.example.com/org/repo:latest

  # Exclude additional paths on the command line
  velda instance export --exclude '/home/user/.cache/*' --exclude '*.log' --push registry.example.com/org/repo:latest

  # Strip all file timestamps for a reproducible image
  velda instance export --strip-times --push registry.example.com/org/repo:latest

Export config (/.dockerexports by default) is a YAML file with the following schema:

  # include: merge additional config files first (local paths or HTTPS URLs)
  include:
    - /etc/shared-export-config.yaml
    - https://example.com/base-export.yaml

  # exclude: glob patterns for paths to omit from the image
  #   leading "/" → absolute (matched from root)
  #   no leading "/" → relative (matched at any depth)
  exclude:
    - /tmp/*           # absolute: only top-level /tmp
    - var/cache/*      # relative: any var/cache at any depth
    - "*.log"          # relative: any .log file anywhere

  # strip_times: set all file timestamps to epoch 0 for reproducible builds
  strip_times: true`,
	RunE: runExportContainer,
}

func init() {
	instanceCmd.AddCommand(exportContainerCmd)
	f := exportContainerCmd.Flags()
	f.StringArrayP("env", "e", nil,
		"Environment variable to bake into the image (NAME or NAME=VALUE); may be repeated")
	f.StringP("push", "p", "", "Push the image to this registry reference (e.g. registry.io/repo:tag)")
	f.StringP("output", "o", "", "Save the image as an OCI-compatible tar file at this path")
	f.String("auth", "docker",
		"Registry auth method: docker (default keychain), google (GCR/AR), manual (stdin prompt)")
	f.String("config", "",
		"Path to the export config YAML file (default /.dockerexports if it exists)")
	f.StringArray("exclude", nil,
		"Additional glob pattern to exclude from the image (may be repeated; appended after config excludes)")
	f.Bool("strip-times", false,
		"Set all file timestamps in the image layer to epoch 0 (overrides the config file setting)")
}

func runExportContainer(cmd *cobra.Command, args []string) error {
	if os.Geteuid() != 0 {
		// Use sudo to re-run the command as root, which is required to read certain files in the container.
		args := append([]string{"sudo", "-E"}, os.Args...)
		envs := append([]string{fmt.Sprintf("_VELDA_IMAGE_UID=%d", os.Getuid()), fmt.Sprintf("_VELDA_IMAGE_GID=%d", os.Getgid())}, os.Environ()...)

		return syscall.Exec("/usr/bin/sudo", args, envs)
	}
	// --- Validation ----------------------------------------------------------
	pushRef, _ := cmd.Flags().GetString("push")
	outputFile, _ := cmd.Flags().GetString("output")
	if pushRef == "" && outputFile == "" {
		return fmt.Errorf("specify at least one of --push or --output")
	}

	// --- Export config -------------------------------------------------------
	configPath, _ := cmd.Flags().GetString("config")
	exportCfg, err := loadExportConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading export config: %w", err)
	}
	// Merge any extra exclude patterns supplied on the command line.
	extraExclude, _ := cmd.Flags().GetStringArray("exclude")
	exportCfg.Exclude = append(exportCfg.Exclude, extraExclude...)
	// --strip-times flag overrides config file when explicitly set (in either direction).
	if f := cmd.Flags().Lookup("strip-times"); f != nil && f.Changed {
		if stripTimesFlag, _ := cmd.Flags().GetBool("strip-times"); true {
			exportCfg.StripTimes = stripTimesFlag
		}
	}

	// --- Auth ----------------------------------------------------------------
	// Resolve credentials up-front so the user is prompted before any slow
	// filesystem work begins.
	authMethod, _ := cmd.Flags().GetString("auth")
	pushOption, err := resolveRegistryAuth(authMethod)
	if err != nil {
		return fmt.Errorf("resolving registry auth: %w", err)
	}

	// --- Environment --------------------------------------------------------
	envFlags, _ := cmd.Flags().GetStringArray("env")
	envVars := resolveContainerEnv(envFlags)

	// --- Entrypoint / CMD ---------------------------------------------------
	var entrypoint, cmdSlice []string
	if len(args) == 0 {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		entrypoint = []string{shell}
	} else {
		entrypoint = []string{args[0]}
		if len(args) > 1 {
			cmdSlice = args[1:]
		}
	}

	// --- UID / GID ----------------------------------------------------------
	uid := os.Getenv("_VELDA_IMAGE_UID")
	gid := os.Getenv("_VELDA_IMAGE_GID")
	// Use env. If not set, default to root assuming user run export with sudo.
	if uid == "" {
		uid = "0"
	}
	if gid == "" {
		gid = "0"
	}
	user := fmt.Sprintf("%s:%s", uid, gid)

	// Bind-mount the root to a temp location so we can reliably walk the filesystem without
	// being overlayed by other mounts.
	tmpMount, err := os.MkdirTemp("/run", "")
	if err != nil {
		return fmt.Errorf("creating temp dir to check mounts: %w", err)
	}
	if err := unix.Mount("/", tmpMount, "", unix.MS_BIND, ""); err != nil {
		os.Remove(tmpMount)
		return fmt.Errorf("bind-mounting root to check mounts: %w", err)
	}
	defer func() {
		unix.Unmount(tmpMount, 0)
		os.Remove(tmpMount)
	}()

	opener := func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			err := buildRootTar(pw, tmpMount, exportCfg.Exclude, exportCfg.StripTimes)
			pw.CloseWithError(err)
		}()
		return pr, nil
	}

	var img v1.Image = empty.Image

	layer, err := tarball.LayerFromOpener(opener)
	if err != nil {
		return fmt.Errorf("creating layer from root tar: %w", err)
	}

	img, err = mutate.AppendLayers(img, layer)
	if err != nil {
		return fmt.Errorf("appending layer: %w", err)
	}

	// --- Image config -------------------------------------------------------
	cfg, err := img.ConfigFile()
	if err != nil {
		return fmt.Errorf("getting config file: %w", err)
	}
	cfg = cfg.DeepCopy()
	cfg.OS = runtime.GOOS
	cfg.Architecture = runtime.GOARCH
	cfg.Config.Env = envVars
	cfg.Config.Entrypoint = entrypoint
	cfg.Config.Cmd = cmdSlice
	cfg.Config.User = user

	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		return fmt.Errorf("setting image config: %w", err)
	}

	// --- Output / Push ------------------------------------------------------

	if pushRef != "" {
		ref, err := name.ParseReference(pushRef)
		if err != nil {
			return fmt.Errorf("Invalid image reference %q: %w", pushRef, err)
		}
		cmd.PrintErrf("Pushing to %s ...\n", ref)
		updateCh := make(chan v1.Update, 1)
		pushErr := make(chan error, 1)
		go func() {
			if err := remote.Write(ref, img, pushOption, remote.WithProgress(updateCh)); err != nil {
				pushErr <- fmt.Errorf("pushing image: %w", err)
			} else {
				pushErr <- nil
			}
		}()
		for u := range updateCh {
			if u.Total > 0 {
				pct := 100 * u.Complete / u.Total
				cmd.PrintErrf("\r  Upload: %s / %s (%d%%)",
					fmtBytes(u.Complete), fmtBytes(u.Total), pct)
			} else if u.Complete > 0 {
				cmd.PrintErrf("\r  Upload: %s", fmtBytes(u.Complete))
			}
		}
		cmd.PrintErrln()
		if err := <-pushErr; err != nil {
			return err
		}
		cmd.PrintErrf("Successfully pushed to %s\n", ref)
		if d, err := img.Digest(); err != nil {
			cmd.PrintErrf("Warning: computing image digest: %v\n", err)
		} else {
			cmd.PrintErrf("Image digest: %s\n", d.String())
		}
	}

	if outputFile != "" {
		tag, err := name.NewTag("packed:latest")
		if err != nil {
			return fmt.Errorf("building default tag: %w", err)
		}
		cmd.PrintErrf("Saving image to %s ...\n", outputFile)
		if err := tarball.WriteToFile(outputFile, tag, img); err != nil {
			return fmt.Errorf("saving image tar: %w", err)
		}
		cmd.PrintErrf("Saved image to %s\n", outputFile)
	}

	return nil
}

// resolveRegistryAuth returns the remote.Option that provides the chosen
// authentication method. For "manual" it reads credentials from stdin before
// returning; if stdin is a terminal the password read is non-echoing.
func resolveRegistryAuth(method string) (remote.Option, error) {
	switch method {
	case "docker", "":
		return remote.WithAuthFromKeychain(authn.DefaultKeychain), nil

	case "google":
		// google.Keychain tries Application Default Credentials, the
		// metadata server, gcloud, etc., then falls back to the Docker keychain.
		kc := authn.NewMultiKeychain(google.Keychain, authn.DefaultKeychain)
		return remote.WithAuthFromKeychain(kc), nil

	case "manual":
		isTTY := term.IsTerminal(int(os.Stdin.Fd()))

		var username, password string

		if isTTY {
			fmt.Fprint(os.Stderr, "Registry username: ")
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return nil, fmt.Errorf("reading username: %w", scanner.Err())
			}
			username = strings.TrimRight(scanner.Text(), "\r\n")

			fmt.Fprint(os.Stderr, "Registry password: ")
			pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr) // newline after the hidden input
			if err != nil {
				return nil, fmt.Errorf("reading password: %w", err)
			}
			password = string(pwBytes)
		} else {
			// Non-interactive: expect two lines on stdin (username then password).
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return nil, fmt.Errorf("reading username from stdin: %w", scanner.Err())
			}
			username = strings.TrimRight(scanner.Text(), "\r\n")
			if !scanner.Scan() {
				return nil, fmt.Errorf("reading password from stdin: %w", scanner.Err())
			}
			password = strings.TrimRight(scanner.Text(), "\r\n")
		}

		auth := authn.FromConfig(authn.AuthConfig{
			Username: username,
			Password: password,
		})
		return remote.WithAuth(auth), nil

	default:
		return nil, fmt.Errorf("unknown auth method %q; valid values: docker, google, manual", method)
	}
}

// resolveContainerEnv converts flag values into KEY=VALUE strings.
// A bare KEY (no '=') is resolved from the current process environment;
// if the variable is not set it is silently omitted.
func resolveContainerEnv(flags []string) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		if strings.ContainsRune(f, '=') {
			// Explicit value – use as-is.
			out = append(out, f)
		} else if val, ok := os.LookupEnv(f); ok {
			out = append(out, f+"="+val)
		}
	}
	return out
}

// inode uniquely identifies a file by device + inode number, used to detect
// hard links.
type inodeKey struct{ dev, ino uint64 }

// buildRootTar walks "/" and writes a tar stream to w, skipping any directory
// (and its subtree) that resides on a different block device than rootDev.
// Paths matching any of the exclusion glob patterns (relative, no leading '/') are omitted.
// When stripTimes is true all file/directory modification and access timestamps
// are set to the Unix epoch (time.Time{}) to produce a reproducible layer.
func buildRootTar(w io.Writer, tmpMount string, excludePatterns []string, stripTimes bool) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	// hardLinks maps an (dev, ino) pair to the first tar path we wrote for
	// that inode so that subsequent occurrences can be recorded as hard links.
	hardLinks := make(map[inodeKey]string)

	return filepath.WalkDir(tmpMount, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Permission denied or similar – skip silently.
			return walkErr
		}

		// Apply exclusion patterns before any stat/io work.
		relPath := strings.TrimPrefix(filepath.ToSlash(path), "/")
		if relPath != "" && matchesAnyExclude(relPath, excludePatterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		var st syscall.Stat_t
		if err := syscall.Lstat(path, &st); err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// For symlinks we need the link target to pass to FileInfoHeader.
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if linkTarget, err = os.Readlink(path); err != nil {
				return err
			}
		}

		hdr, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}

		// Paths inside the tar are relative to the root (no leading "/").
		hdr.Name = strings.TrimPrefix(filepath.ToSlash(path), tmpMount)
		if hdr.Name == "" {
			hdr.Name = "."
		}
		if d.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}

		// Preserve the on-disk owner; clear name-strings (UID/GID are canonical).
		hdr.Uid = int(st.Uid)
		hdr.Gid = int(st.Gid)
		hdr.Uname = ""
		hdr.Gname = ""

		if stripTimes {
			hdr.ModTime = time.Time{}
			hdr.AccessTime = time.Time{}
			hdr.ChangeTime = time.Time{}
		}

		// Detect hard links among regular files.
		if hdr.Typeflag == tar.TypeReg && st.Nlink > 1 {
			key := inodeKey{st.Dev, st.Ino}
			if firstPath, seen := hardLinks[key]; seen {
				hdr.Typeflag = tar.TypeLink
				hdr.Linkname = firstPath
				hdr.Size = 0
				return tw.WriteHeader(hdr)
			}
			hardLinks[key] = hdr.Name
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		// Copy file content for regular files only.
		if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}

		return nil
	})
}

// fmtBytes formats a byte count as a human-readable string (e.g. "1.2 GiB").
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
