---
title: "velda list-session"
---

## velda list-session

List sessions of an instance

### Synopsis

List active sessions of an instance.

Session is an instance with active compute resource attached.

The pool (configured by system administrator) determines the type
and amount of compute resources attached to the session.


```
velda list-session [flags]
```

### Options

```
  -h, --help                  help for list-session
  -i, --instance string       Instance name or ID. Default to the current instance if running in Velda, or default-instance clientlib.
  -o, --output string         Output format. Options: table, json (default "table")
  -s, --service_name string   If specified, only list sessions that are bound to this service.
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

