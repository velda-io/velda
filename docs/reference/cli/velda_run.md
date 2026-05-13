---
title: "velda run"
---

## velda run

Run a command in a session

### Synopsis

Run a command in a session.

If no command is provided, it will start an interactive shell session.

Session is the basic unit of execution in Velda.  It is a collection of
pre-defined compute resources that can be independently attached your
instance. You can run multiple sessions with the same instsance in parallel.


If a session ID is provided, it will attach to the an existing session, or
create a new one if not already exists.

```
velda run [flags] [command] [args...]
```

### Examples

```
velda run -it less path-to-file
```

### Options

```
      --after strings              For batch task, run it after other tasks are finished(either succeeded or failed)
      --after-fail strings         For batch task, run it after other tasks are failed
      --after-success strings      For batch task, run it after other tasks finished successfully (return 0)
      --batch                      Batch mode. Queue the job, and return immediately
      --checkpoint-on-idle         If all connections are closed, check-point the session. Imply keep-alive.
  -C, --directory string           Working directory. Default to current directory if running in Velda and a command is provided, otherwise home directory.
  -e, --env strings                Environment variables to pass to the batch job in KEY=VALUE or KEY form. If VALUE is omitted, use current system value. Only valid for batch mode outside of session.
  -f, --follow                     Follow the execution of the command. Only valid for batch mode.
      --gang                       Enable gang scheduling for the task. Ignored if total shard is 0
  -h, --help                       help for run
      --instance string            Instance name or ID. Default to the current instance if running in Velda, or default-instance config.
      --keep-alive                 Keep the session alive after all processes exits even if all connections are closed.
      --keep-alive-time duration   How long to keep the session alive after all connections are closed. Default to 0, which means no keep-alive.
  -l, --labels strings             Labels for the task
  -n, --name string                Name of the the task
      --new-session                Always create a new session
  -I, --noinput                    Don't take input from stdin. Only used with a command.
  -P, --pool string                Pool to run the workload in (default "shell")
      --priority int               Priority of the task. Lower number means higher priority.
  -p, --publish stringArray        Publish a session's port to the current session. Format: [host:]local_port:session_port
  -q, --quiet                      Suppress all non-error loggings.
  -s, --service-name string        Service name, which can be used to identify session later. Default is ssh if connected externally, or empty.
      --session-id string          Reconnect to specific session. If not provided, it will try to find a session with the service name or create a new session.
      --shell                      Use shell
      --snapshot string            Snapshot name to use. If provided, uses an existing snapshot; if not provided and writable-dirs are specified, a new snapshot will be created.
      --tags string                Tags to set on the session in format key=value,key2=value2. Set value empty to remove tag.
  -N, --total-shards int32         Total number of shards to run. Each shard will have environment variable VELDA_SHARD_ID & VELDA_TOTAL_SHARDS set. Batch job only.
      --tty string                 TTy mode. auto|yes|no. Default to auto, will be based on input. (default "auto")
  -u, --user string                User to run the command as. Ignored if running in a Velda session with a command provided, where it will always use the current user. (default "user")
      --writable-dir strings       Writable directories. If not set, the entire filesystem will be writable if snapshot is not set, otherwise it will be read-only. If set, the session will be created with a snapshot of the current disk, and the specified directories will be writable and sync with the current version.
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

