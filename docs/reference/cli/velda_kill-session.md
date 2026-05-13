---
title: "velda kill-session"
---

## velda kill-session

Kill a session of an instance

### Synopsis

Kill an active session of an instance.
	
This will be done by terminating the compute resources attached to the session.
To soft-terminate the session, use vrun with OS kill command instead.

```
velda kill-session [flags]
```

### Options

```
  -f, --force                 Force kill the session without waiting for cleanup.
  -h, --help                  help for kill-session
  -i, --instance string       Instance name or ID. Default to the current instance if running in Velda, or default-instance clientlib.
  -s, --service-name string   If specified, all sessions with this service name will be killed.
      --session-id string     Specify the session ID to kill.
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

