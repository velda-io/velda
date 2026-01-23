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

### Run a Simple Command

```bash
# This runs on a different machine as your shell.
vrun echo "Hello from Velda!"
```

## Using Resource Pools

Resource pools provide access to different compute configurations. Use the `-P` flag to specify a pool:

```bash
# List available pools
velda pool list

# Run on default pool (shell, typically 1-4 cpus)
vrun python train.py

# Run on CPU-intensive pool
vrun -P cpu-large make -j 16

# Run on GPU pool
vrun -P gpu-t4 python train.py

# Run on high-end GPU pool
vrun -P gpu-4xa100 python train.py --distributed
```

## Working with Sessions

Sessions let you run multiple commands in the same environment and reattach from different terminals.

### Create a Named Session

```bash
# Start a long-running training job in a named session
vrun -s my-training -P gpu-a100 python train.py --epochs 100
```

### Reattach to the Session

From another terminal or after disconnecting:

```bash
# Check GPU usage in the same session
vrun -s my-training nvidia-smi

# View training logs
vrun -s my-training tail -f training.log

# Open an interactive shell in the session
vrun -s my-training bash
```

## Running Interactive Applications

### Jupyter Notebook

```bash
# Connect to instance with port-forwarding enabled.
ssh -o SetEnv VELDA_INSTANCE=$USER-instance velda@$VELDA_IP -L 8888:8888 jupyter notebook --no-browser

# Open http://localhost:8888 in your browser
```

### Data Processing

```bash
# Process large dataset with high CPU count
vrun -P cpu-xlarge python process_data.py \
  --input /data/raw \
  --output /data/processed \
  --workers 32

# Run Spark job
vrun -P cpu-xlarge spark-submit \
  --master local[16] \
  analyze.py
```

### Compilation & Building

```bash
# Compile with many cores
vrun -P cpu-large make -j 16

# Run tests in parallel
vrun -P cpu-large pytest -n 16
```

## Managing Your Environment

### Install Packages

You can install packages normally inside your Velda instance:

```bash
# Python packages
pip install torch torchvision transformers

# System packages (if you have sudo)
sudo apt update && sudo apt install -y htop tmux

# Conda environments
conda create -n myenv python=3.11
conda activate myenv
pip install -r requirements.txt
```

## Accessing Running Services

When a service is running in a session, you can access it multiple ways:

### 1. Use Session Name as Hostname (from within instance)

```bash
# Start service
vrun -s api python -m flask run --host=0.0.0.0 --port 5000 &

# Access from same instance
curl api:5000
```

### 2. Port Forward with `-p` Flag

```bash
# Forward while running
vrun -p 8080:80 -s nginx nginx

# Access on localhost
curl localhost:8080
```

### 3. Port Forward from Local Machine

```bash
# Start service
vrun -s web python -m http.server 8000

# From local machine
velda port-forward -s web --port 8000 -l :8000

# Access at http://localhost:8000
```
