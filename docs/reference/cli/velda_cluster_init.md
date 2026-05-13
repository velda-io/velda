---
title: "velda cluster init"
---

## velda cluster init

Initialize a velda Cluster

### Synopsis


This command initialize a new velda cluster from the current machine.

The sandbox-dir will be the path to the directory where the velda environment will be stored, including config and sandbox data.


```
velda cluster init sandbox-dir [flags]
```

### Options

```
      --backends strings    The backends to enable (comma-separated list, e.g., 'aws,gce') (default [all])
      --base-image string   The docker image to initialize the sandbox (default "ubuntu:24.04")
  -h, --help                help for init
```

### Options inherited from parent commands

```
      --agent-launcher string       The agent launcher to use (docker) (default "docker")
      --config_dir string           config directory. Defaults to env VELDA_CONFIG_DIR or ~/.config/velda
      --debug                       Enable debug mode
      --identity-file string        Path to the private key for SSH authentication
      --jump-identity-file string   Path to the private key for SSH jump server authentication
      --jump-proxy string           SSH jump server in user@host format
      --profile string              The user profile to use.
```

### SEE ALSO

* [velda cluster](velda_cluster.md)	 - Manage velda cluster

