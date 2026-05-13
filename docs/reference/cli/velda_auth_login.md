---
title: "velda auth login"
---

## velda auth login

Login to Velda.

```
velda auth login [flags]
```

### Options

```
      --broker string   Broker address. If profile already exists, this will be ignored and use the existing broker address in config. (default "https://velda.cloud")
  -h, --help            help for login
      --new-profile     Create a new profile if --profile is not provided and no default profile exists.
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

* [velda auth](velda_auth.md)	 - Manage OAuth credentials for the CLI

