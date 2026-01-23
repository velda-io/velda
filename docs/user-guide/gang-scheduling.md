# Gang Scheduling

Gang scheduling ensures all shards of a job start simultaneously or not at all. This is critical for tightly coupled parallel jobs that require synchronization, such as distributed training with collective communication or MPI programs.

## What is Gang Scheduling?

Gang scheduling is an all-or-nothing scheduling policy:

- **Without gang scheduling**: Shards start as resources become available
- **With gang scheduling**: All shards wait until resources for every shard are available, then all start together

## Enabling Gang Scheduling

Add the `--gang` flag when submitting sharded jobs:

```bash
# All 4 shards must start together
vbatch -N 4 --gang --name distributed-training python train.py
```

## When to Use Gang Scheduling

### Required For

**MPI Programs**

MPI jobs require all processes to initialize together:

```bash
# MPI job with 8 processes
vbatch -N 8 --gang -P cpu-8 --name mpi-job python mpi_program.py
```

```python
from mpi4py import MPI

comm = MPI.COMM_WORLD
rank = comm.Get_rank()
size = comm.Get_size()

# All ranks must be present for collective operations
data = comm.gather(local_data, root=0)  # Requires all ranks
```

**Distributed Training with Barriers**

Training frameworks that use synchronization barriers:

```bash
# PyTorch distributed training
vbatch -N 4 --gang -P gpu-1 --name train python train_distributed.py
```

```python
import torch.distributed as dist

# Initialize process group (all ranks must be present)
dist.init_process_group(backend='nccl')

# Barrier synchronization
dist.barrier()  # All processes wait here

# Collective operations
dist.all_reduce(tensor)  # Requires all processes
```

**Jobs with Collective Communication**

Any code using all-reduce, broadcast, scatter/gather, or barriers:

```bash
vbatch -N 8 --gang --name collective-compute python distributed_compute.py
```

### Not Needed For

**Embarrassingly Parallel Workloads**

Independent data processing doesn't need synchronization:

```bash
# Each shard processes files independently
vbatch -N 16 python process_files.py  # No --gang needed
```

**Data Parallelism Without Synchronization**

If shards don't communicate, gang scheduling is unnecessary:

```bash
# Each shard trains independently
vbatch -N 10 python independent_training.py  # No --gang needed
```

**Hyperparameter Sweeps**

Different experiments running in parallel:

```bash
# Each shard tests different hyperparameters
vbatch -N 20 python grid_search.py  # No --gang needed
```

## How Gang Scheduling Works

### Without Gang Scheduling

Shards start as soon as resources are available:

```
Time 0s:   Shard 0 starts (GPU available)
Time 0s:   Shard 1 starts (GPU available)
Time 30s:  Shard 2 starts (GPU became available)
Time 60s:  Shard 3 starts (GPU became available)

❌ Problem: Shards 0-1 may timeout waiting for Shards 2-3 to join
```

### With Gang Scheduling

All shards wait until all resources are available:

```
Time 0s:   Waiting for 4 GPUs...
Time 0s:   2 GPUs available - not enough, continue waiting
Time 30s:  3 GPUs available - not enough, continue waiting  
Time 45s:  4 GPUs available → All shards start simultaneously

✅ Benefit: Synchronized start, no timeouts, no wasted resources
```

## Gang Scheduling and Resource Management

### Resource Reservation

With gang scheduling, resources are reserved atomically:

```bash
# Reserves 8 GPUs atomically
vbatch -N 8 --gang -P gpu-a100 --name training python train.py
```

If only 6 GPUs are available, the job waits rather than starting with partial resources.

### Queuing Behavior

Gang-scheduled jobs may wait longer but avoid wasted resources:

```bash
# This may queue longer but ensures all shards start together
vbatch -N 32 --gang -P cpu-large --name large-simulation python simulate.py
```

**Trade-offs**:
- **Longer queue time**: Must wait for all resources
- **No wasted resources**: Avoids partial starts that fail
- **Guaranteed synchronization**: All shards begin together

## Combining Gang Scheduling with Other Features

### With Resource Pools

```bash
# All shards on A100 GPUs, starting together
vbatch -N 4 --gang -P gpu-a100 --name train python train.py
```

### With Dependencies

```bash
# Preprocess data first
PREP=$(vbatch --name preprocess python preprocess.py)

# Then run gang-scheduled training
vbatch -N 8 --gang --after-success preprocess \
  -P gpu-1 --name train python train_distributed.py
```