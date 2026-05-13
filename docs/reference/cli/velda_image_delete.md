---
title: "velda image delete"
---

## velda image delete

Delete an image

### Synopsis

Delete an image by name.

Examples:
  # Delete an image
  velda image delete my-image

```
velda image delete <image-name> [flags]
```

### Options

```
  -f, --force   Skip confirmation prompt
  -h, --help    help for delete
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

