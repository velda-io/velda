---
title: "velda instance"
---

## velda instance

Manage instances for the current account

### Synopsis

Manage instances for the current account.

Instance is the disk that contains your application and data.
Instance can be attached with multiple compute devices(CPU/Memory/GPU),
each forms a session of that particular instance.

### Options

```
  -h, --help   help for instance
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

* [velda](velda.md)	 - Velda CLI
* [velda instance create](velda_instance_create.md)	 - Create a new instance
* [velda instance delete](velda_instance_delete.md)	 - Delete an instance
* [velda instance export](velda_instance_export.md)	 - Pack current '/' into a container image
* [velda instance list](velda_instance_list.md)	 - List all instances of the current account

