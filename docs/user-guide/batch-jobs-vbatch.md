# Batch Jobs with vbatch

`vbatch` is Velda's command for running non-interactive batch jobs. It's an alias for `velda run --batch`, providing a convenient way to submit jobs that run to completion without requiring an active connection.

## vbatch vs vrun

| Feature | vrun | vbatch |
|---------|------|--------|
| **Interactive** | Yes - maintains session | No - runs to completion |
| **Connection** | Requires active connection | Submit and disconnect |
| **Use Case** | Development, debugging | Production jobs, automation |
| **Logs** | Real-time via terminal | Retrieved via `velda task log` |
| **Sessions** | Named sessions supported | Task-based tracking |
| **Output** | Terminal stdout/stderr | Task ID for tracking |

## Basic Usage

### Submitting a Batch Job

When you submit a batch job, `vbatch` returns a task ID that you can use to track the job:

```bash
# Submit a batch job
vbatch python train.py --epochs 100

# Returns: task-abc123

# Submit to a specific pool
vbatch -P gpu-a100 python train.py --epochs 100

```

### Following Job Execution in Real-Time

Use the `-f` or `--follow` flag to submit a job and automatically stream its logs until completion:

```bash
# Submit and follow logs
vbatch -f python train.py --epochs 100

# This will:
# 1. Submit the job
# 2. Print the task ID
# 3. Stream stdout/stderr in real-time
# 4. Exit when the job completes
```

This is useful when you want fire-and-forget behavior but still want to see the output immediately.
You can abort the streaming at any time once task is submitted, and you may choose to continue run the task or cancel it.

## Managing Tasks

### Viewing Task Details

```bash
# Get task information
velda task get task-abc123

# Get specific fields
velda task get task-abc123 -o status --header=false

# Get full details in JSON
velda task get task-abc123 -o json
```

### Listing Tasks

```bash
# List all top-level tasks
velda task list

# List subtasks of a parent task
velda task list task-abc123

# Search tasks by label
velda task search -l environment=production -l team=ml
```

### Streaming Logs

Velda provides flexible log streaming with automatic stdout/stderr separation:

```bash
# View logs for a completed task
velda task log task-abc123

# Follow logs in real-time (for running tasks)
velda task log -f task-abc123

# Both stdout and stderr are streamed to the appropriate outputs
# - stdout goes to your terminal's stdout
# - stderr goes to your terminal's stderr
```

The log command properly separates stdout and stderr streams, making it easy to redirect them independently:

```bash
# Redirect stdout to file, keep stderr visible
velda task log task-abc123 > output.txt

# Redirect both streams separately
velda task log task-abc123 > output.txt 2> errors.txt
```

### Watching Task Status

Use `velda task watch` to monitor a task's status changes until completion:

```bash
# Watch a task until it completes
velda task watch task-abc123

# This will print status updates as the task transitions through states:
# TASK_STATUS_QUEUEING -> TASK_STATUS_RUNNING -> TASK_STATUS_SUCCESS
```

The watch command is particularly useful for monitoring tasks with subtasks, as it will also show status transitions like `TASK_STATUS_RUNNING_SUBTASKS`.

### Canceling Tasks

```bash
# Cancel a running task
velda task cancel task-abc123

# This will:
# - Terminate the running task
# - Cancel any pending subtasks
# - Mark the task as TASK_STATUS_CANCELLED