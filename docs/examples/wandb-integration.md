# Weights & Biases Integration

This guide demonstrates how to use Weights & Biases (W&B) Sweeps on Velda for hyperparameter optimization with automatic experiment tracking and distributed agent execution.

## Overview

Weights & Biases is a platform for experiment tracking, model optimization, and collaboration in machine learning. W&B Sweeps enable automated hyperparameter search across multiple parallel runs, allowing you to efficiently explore your model's hyperparameter space.

The Velda integration with W&B provides:

- **Distributed sweep agents**: Run multiple hyperparameter search agents in parallel
- **GPU acceleration**: Easy access to GPU resources for training
- **Automatic tracking**: Seamless experiment logging and visualization
- **Resource management**: Efficient allocation of compute resources

## Quick Start

### Prerequisites

Install required dependencies:

```bash
pip install torch torchvision wandb
```

### Authentication

Login to your W&B account:

```bash
wandb login
```

This will prompt you to enter your API key, which can be found at [https://wandb.ai/authorize](https://wandb.ai/authorize).

### Creating a Sweep

1. Create a sweep configuration file (see [Configuration](#configuration) below)

2. Initialize the sweep:

```bash
wandb sweep wdb.yaml
```

This command will return a `SWEEP_ID` that you'll use to launch agents.

### Launching Agents

#### Single Agent

Start a single agent on a GPU instance:

```bash
vrun -P gpu-t4-1 wandb agent <SWEEP_ID>
```

#### Multiple Agents

Launch multiple agents in parallel for faster hyperparameter search:

```bash
vbatch -N 2 -P gpu-t4-1 wandb agent <SWEEP_ID>
```

This command:

- Launches 2 agents (`-N 2`)
- Uses the GPU T4 resource pool (`-P gpu-t4-1`)
- Each agent will sample different hyperparameters
- Returns a task ID for tracking and management

### Monitoring Progress

After launching agents, you'll receive a job ID that you can use to manage jobs.

The Parallel Coordinates plot in W&B will populate after multiple runs complete, showing relationships between hyperparameters and metrics.

### Managing Jobs

Cancel running agents:

```bash
velda task cancel <job_id>
```

Or use the cancel button in the Velda UI.

## Configuration

### Sweep Configuration File

Create a `wdb.yaml` file defining your hyperparameter search space:

```yaml
program: wdb_run_train.py
method: random
metric:
  goal: minimize
  name: loss
parameters:
  batch_size:
    distribution: q_log_uniform_values
    max: 256
    min: 32
    q: 8
  dropout:
    values:
    - 0.3
    - 0.4
    - 0.5
  fc_layer_size:
    values:
    - 128
    - 256
    - 512
  learning_rate:
    distribution: uniform
    max: 0.1
    min: 0
  optimizer:
    values:
    - adam
    - sgd
  epochs:
    values:
    - 1
command:
  - ${env}
  - ${interpreter}
  - ${program}
  - ${args}
```

Refer to [wandb documents](https://docs.wandb.ai/) for more details about the configurations.

## Example Workflow

Here's a complete workflow from start to finish:

```bash
# 1. Install dependencies
pip install torch torchvision wandb

# 2. Login to W&B
wandb login

# 3. Create your training script and config
# (wdb_run_train.py and wdb.yaml)

# 4. Test locally
python wdb_run_train.py

# 5. Create the sweep
wandb sweep --name "mnist-hyperparam-search" wdb.yaml
# Output: wandb: Created sweep with ID: abc123def
# Output: wandb: View sweep at: https://wandb.ai/...

# 6. Launch multiple agents
vbatch -N 4 -P gpu-t4-1 wandb agent your-entity/your-project/abc123def
# Output: Task ID: task_xyz789

# 7. Monitor in W&B dashboard and Velda UI

# 8. When satisfied, cancel remaining runs
velda task cancel task_xyz789

# 9. Analyze results in W&B and export best config
```

## Summary

Velda's integration with Weights & Biases provides a powerful platform for hyperparameter optimization:

- **Easy parallelization**: Launch multiple agents with a single command
- **GPU acceleration**: Seamless access to GPU resources
- **Comprehensive tracking**: Automatic logging of experiments
- **Cost efficiency**: Pay only for compute time used

By following this guide, you can efficiently explore hyperparameter spaces, track experiments, and optimize your machine learning models using Velda's distributed compute platform with W&B's experiment management tools.

For more examples and advanced use cases, visit:
- [W&B Documentation](https://docs.wandb.ai/)
- [W&B Sweeps Guide](https://docs.wandb.ai/guides/sweeps)
