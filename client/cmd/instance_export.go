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
	"bufio"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
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
		"Environment variable to bake into the image (*, NAME or NAME=VALUE); may be repeated")
	f.StringP("push", "p", "", "Push the image to this registry reference (e.g. registry.io/repo:tag)")
	f.String("auth", "docker",
		"Registry auth method: docker (default keychain), google (GCR/AR), manual (stdin prompt)")
	f.String("config", "",
		"Path to the export config YAML file (default /.dockerexports if it exists)")
	f.StringArray("exclude", nil,
		"Additional glob pattern to exclude from the image (may be repeated; appended after config excludes)")
	f.Bool("strip-times", false,
		"Set all file timestamps in the image layer to epoch 0 (overrides the config file setting)")
	f.Int64("max-layer-size", defaultLayerSizeThreshold,
		"Maximum uncompressed bytes per layer before a new layer is started (0 = single layer, default 200 MiB)")
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
	if pushRef == "" {
		return fmt.Errorf("push reference is required (e.g. --push registry.io/repo:tag)")
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

	// --- Layering ----------------------------------------------------------
	cmd.PrintErrln("Computing image layers...")
	maxLayerSize, _ := cmd.Flags().GetInt64("max-layer-size")
	layerSpecs, err := computeLayers(tmpMount, exportCfg.Exclude, exportCfg.StripTimes, maxLayerSize)
	if err != nil {
		return fmt.Errorf("computing layers: %w", err)
	}

	cmd.PrintErrf("Packing %d layer(s):\n", len(layerSpecs))

	// Parse the push reference early so we can pass the repository to the
	// upload goroutines. The reference is also reused for the manifest push.
	var parsedRef name.Reference
	parsedRef, err = name.ParseReference(pushRef)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", pushRef, err)
	}

	resolvedLayers := make([]v1.Layer, len(layerSpecs))

	// ── Parallel upload path ──────────────────────────────────────────────────
	// Each layer is uploaded concurrently (up to uploadConcurrency at once).
	// stream.NewLayer compresses and hashes in a single read pass; remote.WriteLayer
	// performs an internal HEAD /blobs/{digest} check and skips upload if the
	// blob is already present.
	const uploadConcurrency = 4
	sem := make(chan struct{}, uploadConcurrency)

	uploadProg := mpb.New(mpb.WithOutput(os.Stderr))
	uploadErrors := make([]error, len(layerSpecs))
	var wg sync.WaitGroup

	oldOutput := log.Writer()
	log.SetOutput(uploadProg)
	for i, spec := range layerSpecs {
		i, spec := i, spec
		label := fmt.Sprintf("  [%d] %-30s", i+1, spec.Description)

		merkle := spec.MerkleHash
		repo := parsedRef.Context()
		cached := atomic.Bool{}

		bar := uploadProg.AddSpinner(0,
			mpb.PrependDecorators(
				decor.Name(label, decor.WCSyncSpaceR),
			),
			mpb.AppendDecorators(
				decor.CurrentKibiByte(" % .2f"),
				decor.Name(" "),
				decor.OnCompleteMeta(decor.EwmaSpeed(decor.SizeB1024(0), "% .2f", 30, decor.WCSyncSpace),
					func(_ string) string {
						if cached.Load() {
							return "Cached"
						}
						return "Done"
					},
				),
			),
		)
		progress := make(chan v1.Update, 100)

		updateDone := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			defer close(updateDone)

			last := time.Now()
			for update := range progress {
				newT := time.Now()
				bar.EwmaSetCurrent(int64(update.Complete), newT.Sub(last))
				last = newT
			}
		}()
		go func() {
			defer wg.Done()

			// ── 1. Cached path ──────────────────────────────────────────────────────
			if merkle != "" {
				if digestID, ok := readLayerXattrCache(spec.RootDir, merkle); ok {
					dig := repo.Digest(digestID.String())
					if layer, err := remote.Layer(dig, pushOption); err == nil {
						if exists, err := partial.Exists(layer); err == nil && exists {
							resolvedLayers[i] = layer
							close(progress) // no progress for cached layers
							cached.Store(true)
							bar.SetTotal(spec.TotalSize, true)
							return
						}
					}
					// Any error means the cached digest is stale or unusable;
					// discard it and fall through to a fresh stream upload.
				}
			}
			sem <- struct{}{}
			defer func() { <-sem }()

			layer, err := uploadLayerSpec(spec, parsedRef.Context(), pushOption, progress)
			<-updateDone // ensure all progress updates are processed before marking the bar complete
			if err != nil {
				bar.Abort(false)
			} else {
				bar.SetTotal(-1, true)
			}
			resolvedLayers[i] = layer
			uploadErrors[i] = err
		}()
	}

	wg.Wait()
	uploadProg.Wait()
	log.SetOutput(oldOutput)

	for i, uploadErr := range uploadErrors {
		if uploadErr != nil {
			return fmt.Errorf("uploading layer %d (%s): %w", i+1, layerSpecs[i].Description, uploadErr)
		}
	}

	// --- Build image from resolved layers -----------------------------------
	var img v1.Image = empty.Image
	for i, layer := range resolvedLayers {
		img, err = mutate.Append(img, mutate.Addendum{
			Layer: layer,
			History: v1.History{
				CreatedBy: layerSpecs[i].Description,
			},
		})
		if err != nil {
			return fmt.Errorf("appending layer %d (%s): %w", i+1, layerSpecs[i].Description, err)
		}
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
	// current dir
	cfg.Config.WorkingDir = os.Getenv("PWD")

	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		return fmt.Errorf("setting image config: %w", err)
	}

	// --- Push ------------------------------------------------------

	// Layer blobs were already uploaded above; push only the manifest + config.
	cmd.PrintErrf("Pushing manifest to %s...\n", parsedRef)
	if err := remote.Write(parsedRef, img, pushOption); err != nil {
		return fmt.Errorf("pushing image manifest: %w", err)
	}
	cmd.PrintErrf("Successfully pushed to %s\n", parsedRef)
	if d, err := img.Digest(); err != nil {
		cmd.PrintErrf("Error computing image digest: %v\n", err)
		return err
	} else {
		cmd.Println(parsedRef.Context().Digest(d.String()).String())
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
		if f == "*" {
			// Special case: include all env vars if the user specified "*".
			// Continue adding more overrides.
			out = append(out, os.Environ()...)
		} else if strings.ContainsRune(f, '=') {
			// Explicit value – use as-is.
			out = append(out, f)
		} else if val, ok := os.LookupEnv(f); ok {
			out = append(out, f+"="+val)
		}
	}
	return out
}
