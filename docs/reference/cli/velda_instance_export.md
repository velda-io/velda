---
title: "velda instance export"
---

## velda instance export

Pack current '/' into a container image

### Synopsis

Pack the current root filesystem into a container image.

Paths on different devices/mounts are skipped automatically (e.g. /proc, /sys,
/dev, and any bind-mounts that live on separate block devices).

Environment variables can be specified as:
  NAME        take the value from the current process environment
  NAME=VALUE  use the given value explicitly

The first positional argument is the container entrypoint; all remaining
positional arguments become CMD.  If no positional arguments are given the
value of $SHELL (falling back to /bin/sh) is used as the entrypoint.

The image is built for the current OS/arch (`runtime.GOOS/runtime.GOARCH`) and
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
  strip_times: true

```
velda instance export [flags] [-- entrypoint [args...]]
```

### Options

```
      --auth string           Registry auth method: docker (default keychain), google (GCR/AR), manual (stdin prompt) (default "docker")
      --config string         Path to the export config YAML file (default /.dockerexports if it exists)
  -e, --env stringArray       Environment variable to bake into the image (*, NAME or NAME=VALUE); may be repeated
      --exclude stringArray   Additional glob pattern to exclude from the image (may be repeated; appended after config excludes)
  -h, --help                  help for export
      --max-layer-size int    Maximum uncompressed bytes per layer before a new layer is started (0 = single layer, default 200 MiB) (default 209715200)
  -p, --push string           Push the image to this registry reference (e.g. registry.io/repo:tag)
      --strip-times           Set all file timestamps in the image layer to epoch 0 (overrides the config file setting)
```

### Options inherited from parent commands

```
      --config_dir string           config directory. Defaults to env VELDA_CONFIG_DIR or ~/.config/velda
      --debug                       Enable debug mode
      --identity-file string        Path to the private key for SSH authentication
      --jump-identity-file string   Path to the private key for SSH jump server authentication
      --jump-proxy string           SSH jump server in user@host format
      --profile string              The user profile to use.
```

### SEE ALSO

* [velda instance](velda_instance.md)	 - Manage instances for the current account

