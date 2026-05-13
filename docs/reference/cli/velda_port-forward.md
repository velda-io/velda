---
title: "velda port-forward"
---

## velda port-forward

Establiash a tunnel for forwarding TCP connections

```
velda port-forward [-i instance] [-s session] [-l localhost:port] -p remote-port [flags]
```

### Examples

```
velda port-forward -s ssh -l localhost:2222 -p 22
```

### Options

```
  -h, --help                  help for port-forward
  -i, --instance string       Instance name or ID
  -l, --local string          Local address (default ":0")
      --pool string           Pool name to create session if session does not exist. (default "shell")
  -p, --port int              Remote port
  -s, --service-name string   Service name, which can be used to identify session later.
  -u, --user string           User to login as (default "user")
  -W, --write-direct          Directly use STDIN/STDOUT for operations.
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

