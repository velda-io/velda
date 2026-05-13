---
title: "velda snapshot create"
---

## velda snapshot create

Create a new snapshot of an instance

```
velda snapshot create [-i instance] <name> [flags]
```

### Options

```
  -h, --help              help for create
  -i, --instance string   Instance to snapshot
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

* [velda snapshot](velda_snapshot.md)	 - Manage snapshots of an instance

