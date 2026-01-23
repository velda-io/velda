# Snapshots and Writable Overlays

A snapshot provide similar concept to a container image to make your job reproducible.

Snapshots and writable overlays provide isolated, copy-on-write filesystems for batch jobs. 
This freeze your code & environment when the jobs starts, to allow you to continue iteration while a job is still in progress.
The writable dirs allows you to access the output of your job, or share data with jobs.

## Overview

Snapshots provide a read-only base plus per-job writable overlays so you can
run jobs against a stable, shared environment while continuing to make local
changes on the instance (for example, checking out a different git branch)
without affecting active jobs. This is similar to container is setup with image.

- `--snapshot`: reference a named, read-only base snapshot used by the job.
- `--writable-dir`: directories that are synced from the current instance at
  job start and exposed as writable for that job. Changes inside these paths
  persist according to snapshot semantics.

Important behavior:
- Writable directories are populated (synced) from the instance when the job
  starts so the job sees the current instance state in those paths.
- Changes outside `--writable-dir` are placed into the job's overlay and are
  local to that job; they do not modify the shared base snapshot or affect
  other running jobs.
- If `--snapshot` and `--writable-dir` are both not specified, all directories are writable.
- With default `zfs` based storage driver, creating snapshot should be instant.

## Basic Concepts

### Default Filesystem Behavior

Without any snapshot flags:

```bash
# Entire filesystem is writable
vbatch python train.py
```

All changes are visible to other jobs and persist after completion in both direction.
If you change code, like checking out to another branch, unexpected behavior may happen.

### Writable Directories

Specify which directories should be writable:

```bash
# Only /tmp and /data/output are writable
vbatch --writable-dir /data/output python job.py
```

This creates a snapshot-based filesystem where:
- Specified directories are writable (changes persist)
- Changes to other directories are discarded at job completion.

Snapshot will be taken when the job starts, and deleted when the job is done.

### Named Snapshots

Create and reuse named snapshots:

```bash
# Create a snapshot with current code & data
velda snapshot create base-v1

# Reuse snapshot for future jobs
vbatch --snapshot base-v1 --writable-dir /results python train.py
```

## Use Cases

### Isolating Experiment Outputs

Keep each experiment's output separate while sharing input data:

```bash
# Experiment 1: isolated output directory
vbatch --writable-dir /experiments/exp1 --name exp1 \
  python train.py --config exp1.yaml

# You're now allowed to make any code change here.
# Experiment 2: different isolated output
vbatch --writable-dir /experiments/exp2 --name exp2 \
  python train.py --config exp2.yaml
```

Example directory structure:

```
/experiments/
  exp1/                 # Writable for experiment 1
    checkpoints/
    logs/
  exp2/                 # Writable for experiment 2
    checkpoints/
    logs/
```

### Reproducible Environments

Reproduce previous run jobs

```bash
velda snapshot create job-v1

# Rerun the job with same code, packages and params.
vbatch --snapshot job-v1 python train.py
```
