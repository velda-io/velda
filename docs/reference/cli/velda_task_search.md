---
title: "velda task search"
---

## velda task search

Search tasks by label filters

```
velda task search [flags]
```

### Options

```
      --header              Show header (default true)
  -h, --help                help for search
  -l, --label strings       Label filters (repeatable)
      --max-results int32   Max results
  -o, --output string       Output format (json|yaml|[[<fields>=]<path>]*)
      --page-token string   Page token
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

* [velda task](velda_task.md)	 - Manage tasks

