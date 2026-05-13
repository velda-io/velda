---
title: "velda instance create"
---

## velda instance create

Create a new instance

```
velda instance create [-i <image> | -f instance | -d docker-image] <instance> [flags]
```

### Options

```
  -d, --docker-image string    Docker image to initialize the instance from (e.g., ubuntu:24.04)
      --follow                 Wait for docker-image initialization task and stream status/logs (default true)
  -f, --from-instance string   Name of the instance to clone from
  -h, --help                   help for create
  -i, --image string           Name of the image to create the instance from
      --no-init                Skip running the initialization script when creating from a Docker image
  -q, --quiet                  Suppress status output (task ID is still printed)
      --region string          Region to create the instance in (defaults to current region)
      --snapshot string        Name of the snapshot to create the instance from. If not provided, it will create one from the current instance disk using timestamped-name.
      --tar-file string        Path to local tar file to initialize the instance from
  -v, --verbose                Enable verbose output during instance creation
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

