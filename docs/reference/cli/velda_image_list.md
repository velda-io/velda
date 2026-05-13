---
title: "velda image list"
---

## velda image list

List available images

### Synopsis

List all available images, optionally filtered by prefix.

Examples:
  # List all images
  velda image list

  # List images with the "ubuntu" prefix
  velda image list ubuntu

```
velda image list [prefix] [flags]
```

### Options

```
  -h, --help   help for list
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

