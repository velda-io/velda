# Running Workloads in Velda

This guide explains how to run workloads in Velda using the `vrun` command, including running on different resource pools, reattaching to sessions, and connecting to open ports.

## The `vrun` Command

`vrun` is Velda's primary command for executing workloads on cloud resources. Prefix any command with `vrun` to run it on your Velda Infrastructure after connected to your Velda instance.

`vrun` will run the command on a different node with different compute setup, while keep the environment (including files, packages, environment variables) same as your instance.

### Basic Syntax

```bash
vrun [OPTIONS] COMMAND [ARGS...]
```

## Resource Pools

Resource pools define pre-configured compute profiles. Use the `-P` flag to specify which pool to use.

The available pool configurations vary by cluster.

### Using Resource Pools

```bash
# List available pools
velda pool list

# All pool names below are for reference only.

# Run CPU-intensive compilation
vrun -P cpu-large make -j 16

# Run GPU training
vrun -P gpu-t4 python train.py

# High-memory data processing
vrun -P mem-xlarge python process_large_dataset.py

# Multi-GPU distributed training
vrun -P gpu-4xa100 torchrun --nproc_per_node=4 train.py
```

### Default Pool

If you don't specify a pool, `vrun` uses the default `shell` pool, which typically provides a single CPU instance.

```bash
# Runs on default pool
vrun echo "Hello World"
```

## Working with Sessions

Sessions allow you to run multiple commands in the same physical VM and reattach to running workloads.

### Creating Named Sessions

Use the `-s` flag to assign a custom session name:

```bash
# Start a workload in a named session
vrun -s training -P gpu-a100 python train.py
```

### Reattaching to Sessions

Run additional commands in the same session from any terminal:

```bash
# Check GPU usage in the training session
vrun -s training nvidia-smi

# Open an interactive shell
vrun -s training bash
```

### Session Characteristics

- Commands in the same session run on the same VM
- Multiple commands share allocated resources
- Sessions terminates when disconnected