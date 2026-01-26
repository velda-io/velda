# 5-Minute Quickstart

This guide shows how to use `vrun` to execute commands on remote resources.

## Prerequisites

- Connected to your Velda instance, and run from the terminal.
```bash
ssh -o SetEnv VELDA_INSTANCE=$USER-instance velda@$VELDA_IP
```

Please refer to your cluster instruction or [this guide](../deployment/connecting-to-cluster.md) on how to connect to your instance.

## Your First `vrun` Command

The `vrun` command executes commands on remote resources.

```bash
# This runs on a different machine as your shell.
vrun echo "Hello from Velda!"
```

Use `vbatch` to run the command in the background:
```bash
# Run in the background
JOB_ID=$(vbatch ./long-jobs.sh)
# View logs
velda tasks log ${JOB_ID}
```

## Using Resource Pools

Resource pools provide access to different compute configurations. Consult your cluster admin for pool configuration. Use the `-P` flag to specify a pool:

```bash
# List available pools
velda pool list

# Run on default pool (shell, typically 1-4 cpus)
vrun python train.py

# Run on CPU-intensive pool
vrun -P cpu-large make -j 16

# Run on GPU pool in the background
vbatch -P gpu-t4 python train.py

# Run on high-end GPU pool
vbatch -P gpu-a100-8 python train.py --distributed
```

## Install Packages

Velda guarantees your environment will be consistent when you scale, so you can install packages normally inside your Velda instance:

```bash
# Python packages
pip install torch torchvision transformers

# System packages
sudo apt update && sudo apt install -y htop

# Conda environments
conda create -n myenv python=3.11
conda activate myenv
pip install -r requirements.txt
```
