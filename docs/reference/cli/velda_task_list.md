---
title: "velda task list"
---

## velda task list

List sub-tasks of a task (or top-level tasks if no parent provided)

```
velda task list [parent-task-id] [flags]
```

### Options

```
  -a, --all                 Fetch all results
      --header              Show header (default true)
  -h, --help                help for list
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

