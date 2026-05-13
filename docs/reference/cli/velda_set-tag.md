---
title: "velda set-tag"
---

## velda set-tag

Set or update tags for a session

### Synopsis

Set or update tags for a session.

Tags are key-value pairs that can be used to organize and categorize sessions.
To remove a tag, set its value to an empty string.

Examples:
  # Set tags for a session
  velda set-tag --session-id=abc123 --tags=env=prod,team=ml

  # Remove a tag by setting empty value
  velda set-tag --session-id=abc123 --tags=env=,team=ml


```
velda set-tag --session-id=<session-id> --tags=key=value,key2=value2 [flags]
```

### Options

```
  -h, --help                help for set-tag
  -i, --instance string     Instance name or ID. Default to the current instance if running in Velda, or default-instance clientlib.
  -q, --quiet               Suppress output messages.
      --session-id string   Specify the session ID to update.
      --tags string         Tags to set in format key=value,key2=value2. Set value to empty string to remove a tag.
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

