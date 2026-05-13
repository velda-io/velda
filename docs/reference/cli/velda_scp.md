---
title: "velda scp"
---

## velda scp

Copy files between local and remote machines

```
velda scp SOURCE... DESTINATION [flags]
```

### Examples

```
velda scp local_file.txt instance:/path/to/destination/
velda scp instance:/path/to/file.txt local_destination/
```

### Options

```
      --follow-symlinks       Follow symlinks and copy the target file/directory instead of the symlink itself
  -h, --help                  help for scp
      --pool string           Pool name to create session if session does not exist. (default "shell")
  -p, --preserve              Preserve file mode, owner, and times
      --preserve-mode         Preserve file mode (permissions)
      --preserve-owner        Preserve file owner
      --preserve-times        Preserve file modification and access times
  -r, --recursive             Recursively copy entire directories
  -s, --service-name string   Service name, which can be used to identify session later.
  -u, --user string           User to login as (default "user")
  -v, --verbose               Verbose mode: print detailed messages
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

