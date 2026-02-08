# Ray Autoscaled Cluster

This guide demonstrates how to quickly set up distributed job processing with Ray on Velda, leveraging automatic scaling capabilities without managing clusters or dependencies manually.

## Overview

Ray is a unified framework for scaling AI and Python applications. With Velda's integration, you can run Ray clusters with automatic node provisioning and scaling, allowing you to focus on your workloads rather than infrastructure management.

The Velda Ray integration provides:

- **Automatic scaling**: Workers are added and removed based on demand
- **Multi-pool support**: Separate CPU and GPU worker pools
- **DNS integration**: Head node accessible via predictable DNS names
- **Zero configuration**: No manual cluster management required

## Quick Start with Auto-Scaling

### Prerequisites

Install Ray with the Velda autoscaler add-on:

```bash
pip install ray[default] ray-velda
```

### Starting the Cluster

1. Create a Ray configuration file (see [Configuration](#configuration) below)

2. Start the cluster:

```bash
ray up ray_config.yaml
```

This command will:
- Launch the head node
- Configure autoscaling parameters
- Set up worker pools for CPU and GPU tasks

### Submitting Jobs

Once the cluster is running, submit jobs using the Ray client:

```bash
RAY_ADDRESS=ray://ray-velda:10001 python ray_job.py
```

For GPU-enabled tasks:

```python
import ray
import time

# Initialize Ray
ray.init()

@ray.remote(num_gpus=1)
def gpu_task():
    import torch
    return torch.cuda.get_device_name(0)

if __name__ == "__main__":
    # Launch tasks in parallel (this will use multiple workers)
    futures = [gpu_task.remote()]

    # Gather results
    results = ray.get(futures)

    print("Results:", results)
```

```bash
RAY_ADDRESS=ray://ray-velda:10001 python ray_job_gpu.py
```

### Accessing the Dashboard

For hosted or enterprise, the Ray dashboard is accessible at:

```
https://8265-ray-velda-[instanceid].i.[velda-domain]
```

For open-source, port-forwarding is required. Check [port-forwarding](../user-guide/port-forwarding.md) for details.

### Tearing Down

**Important**: Always tear down the cluster when finished to avoid ongoing charges:

```bash
ray down ray_config.yaml
```

If you don't shut down the cluster, the head node will continue to run and bill your usage.

## Configuration

### Ray Configuration File

Create a `ray_config.yaml` file with the following structure:

```yaml
# The cluster name.
# The head-node can be accessible with ray-[clustername] as DNS name within Velda.
cluster_name: velda

# Use a custom node provider
provider:
  type: external                 # "external" = custom provider
  module: ray_velda.VeldaNodeProvider  # python module path with your NodeProvider subclass

use_internal_ips: true

auth: {}

# Node types & scaling bounds
available_node_types:
  ray.head.default:
    resources: {"CPU": 4}
    max_workers: 1
    node_config: {"pool": "cpu-4"}
  
  worker.default:
    resources: {"CPU": 1}
    min_workers: 0
    max_workers: 2
    node_config: {"pool": "shell"}
  
  worker.gpu:
    resources: {"CPU": 4, "GPU": 1}
    min_workers: 0
    max_workers: 2
    node_config: {"pool": "gpu-t4-1"}

head_node_type: ray.head.default
max_workers: 50

# Global autoscaler knobs (optional)
upscaling_speed: 1.0
idle_timeout_minutes: 10

# Empty setup commands for cluster launcher's checking
initialization_commands: []
setup_commands: []
head_setup_commands: []
worker_setup_commands: []
file_mounts: {}
cluster_synced_files: []

# The autoscaling-config must have same absolute path as current file.
head_start_ray_commands: 
  - 'ray start --head --autoscaling-config ~/velda-demo/ray_config.yaml --port=6379'

worker_start_ray_commands: 
  - 'ray start --address=$RAY_HEAD_IP:6379'
```

### Configuration Parameters Explained

#### Cluster Identity

- **`cluster_name`**: Name of your Ray cluster. The head node will be accessible at `ray-[clustername]` as a DNS name within Velda.

#### Provider Configuration

- **`provider.type`**: Set to `external` to use the custom Velda provider
- **`provider.module`**: Python module path (`ray_velda.VeldaNodeProvider`)

#### Node Types

Define different node types for various workloads:

`node_config.pool`: Velda pool to use to start the worker.
`resources`: Resource spec for Ray to autoscale. Not used by Velda.

Refer to [Ray autoscaling docs](https://docs.ray.io/en/latest/cluster/vms/user-guides/configuring-autoscaling.html) for more parameters.

## Manual Cluster Setup (Without Auto-Scaling)

For more control or testing purposes, you can start a Ray cluster manually without the autoscaling add-on.

### Start Head Node

```bash
vrun -s ray --keep-alive --tty=no ray start --head
```

This command:
- Creates a service named `ray`
- Keeps the head node alive
- Disables TTY allocation
- Starts Ray in head mode

### Start Worker Nodes

```bash
RAY_JOB_ID=$(vbatch -N 2 ray start --block --address=ray:6279)
```

This command:
- Launches 2 worker nodes (`-N 2`)
- Connects workers to the head at `ray:6279`
- Returns a job ID for later management

### Submit Jobs

```bash
RAY_ADDRESS=ray://ray:10001 python ray_job.py
```

### Delete Workers

When finished, cancel the worker jobs:

```bash
velda task cancel ${RAY_JOB_ID}
```

## DNS Naming Convention

The head node is accessible within Velda using the following DNS pattern:

```
ray-[clustername]
```

For example, with `cluster_name: velda`, the head node is accessible at:
- Ray client: `ray://ray-velda:10001`
- Internal communication: `ray-velda:6379`

# Summary

Velda's Ray integration provides a powerful platform for distributed computing with:

- Seamless autoscaling based on workload demand
- Support for heterogeneous compute resources (CPU and GPU)
- Minimal configuration and management overhead
- Built-in DNS and service discovery

By following this guide, you can quickly deploy production-ready Ray clusters that scale automatically, optimize costs, and simplify infrastructure management.

For more examples and use cases, visit the [Velda Examples Repository](https://github.com/velda-io/examples).
