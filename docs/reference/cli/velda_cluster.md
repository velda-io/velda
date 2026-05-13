---
title: "velda cluster"
---

## velda cluster

Manage velda cluster

### Synopsis

This command allows you to manage velda cluster, including starting, stopping, and configuring the velda environment.

### Options

```
      --agent-launcher string   The agent launcher to use (docker) (default "docker")
  -h, --help                    help for cluster
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
* [velda cluster down](velda_cluster_down.md)	 - Bring down a velda cluster
* [velda cluster init](velda_cluster_init.md)	 - Initialize a velda Cluster
* [velda cluster up](velda_cluster_up.md)	 - Bring up a Velda cluster

