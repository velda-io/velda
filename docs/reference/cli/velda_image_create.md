---
title: "velda image create"
---

## velda image create

Create a new image

### Synopsis

Create a new image from an instance and optionally a specific snapshot.

Examples:
  # Create an image from the current state of an instance
  velda image create my-image -i my-instance

  # Create an image from a specific snapshot of an instance
  velda image create my-image -i my-instance -s my-snapshot

```
velda image create <image-name> -i <instance> [-s <snapshot>] [flags]
```

### Options

```
  -h, --help              help for create
  -i, --instance string   Instance to create the image from
  -s, --snapshot string   Snapshot to create the image from
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

* [velda image](velda_image.md)	 - Manage images for the current account

