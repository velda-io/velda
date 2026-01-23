# Sharded Jobs

Sharding allows you to run the same job across multiple parallel instances, with each shard processing a portion of the data. This is ideal for embarrassingly parallel workloads and data parallelism.

## Basic Sharding

Use the `-N` flag to specify the number of shards:

```bash
# Run 4 parallel shards
vbatch -N 4 --name parallel-job python process_data.py
```

Each shard automatically receives environment variables identifying its position:

```python
import os

shard_id = int(os.environ['VELDA_SHARD_ID'])        # 0, 1, 2, or 3
total_shards = int(os.environ['VELDA_TOTAL_SHARDS']) # 4

# Process only this shard's portion of data
for i in range(shard_id, len(data), total_shards):
    process(data[i])
```

## Use Cases

### Data Parallelism

Process large datasets by dividing them across shards:

```bash
# Process 1M records using 8 shards
vbatch -N 8 --name process-records python process_records.py --total 1000000
```

Each shard processes 1/8th of the data:

```python
import os

shard_id = int(os.environ['VELDA_SHARD_ID'])
total_shards = int(os.environ['VELDA_TOTAL_SHARDS'])
total_records = 1_000_000

# Calculate this shard's range
records_per_shard = total_records // total_shards
start = shard_id * records_per_shard
end = start + records_per_shard if shard_id < total_shards - 1 else total_records

print(f"Shard {shard_id} processing records {start} to {end}")
process_records(start, end)
```

### Distributed Training

Run data-parallel training across multiple GPUs:

```bash
# Train on 4 GPUs with data parallelism
vbatch -N 4 -P gpu-1 --name distributed-training python train_distributed.py
```

Example using PyTorch:

```python
import os
import torch
import torch.distributed as dist

# Use VELDA_SHARD_ID as the rank
rank = int(os.environ['VELDA_SHARD_ID'])
world_size = int(os.environ['VELDA_TOTAL_SHARDS'])

# Initialize distributed training
dist.init_process_group(backend='nccl', rank=rank, world_size=world_size)

# Train with DistributedDataParallel
model = torch.nn.parallel.DistributedDataParallel(model)
train(model)
```

### Parameter Sweeps

Search hyperparameter space in parallel:

```bash
# Each shard tests different hyperparameters
vbatch -N 10 --name hyperparam-sweep python hyperparam_search.py
```

```python
import os

shard_id = int(os.environ['VELDA_SHARD_ID'])
total_shards = int(os.environ['VELDA_TOTAL_SHARDS'])

# Define hyperparameter space
learning_rates = [1e-5, 1e-4, 1e-3, 1e-2, 1e-1]
batch_sizes = [16, 32, 64, 128]

# Create all combinations
configs = [(lr, bs) for lr in learning_rates for bs in batch_sizes]

# Each shard tests a subset of configurations
for i in range(shard_id, len(configs), total_shards):
    lr, batch_size = configs[i]
    print(f"Testing lr={lr}, batch_size={batch_size}")
    train_and_evaluate(lr, batch_size)
```

### File Processing

Process multiple files in parallel:

```bash
# List files and process with sharding
vbatch -N 8 --name process-files python process_files.py /data/*.csv
```

```python
import os
import sys
import glob

shard_id = int(os.environ['VELDA_SHARD_ID'])
total_shards = int(os.environ['VELDA_TOTAL_SHARDS'])

# Get all files
files = sorted(glob.glob('/data/*.csv'))

# Process only this shard's files
for i in range(shard_id, len(files), total_shards):
    print(f"Processing {files[i]}")
    process_file(files[i])
```

## Sharded Testing (pytest)

You can run your test suite in parallel across shards by having pytest select a subset of collected tests per shard. A simple and reliable approach is to partition the collected items in `conftest.py` using the `VELDA_SHARD_ID` and `VELDA_TOTAL_SHARDS` environment variables.

conftest.py:

```python
import os

def pytest_collection_modifyitems(config, items):
    shard_id = int(os.environ.get('VELDA_SHARD_ID', '0'))
    total_shards = int(os.environ.get('VELDA_TOTAL_SHARDS', '1'))
    # Keep only items that belong to this shard (round-robin by index)
    selected = [item for idx, item in enumerate(items) if idx % total_shards == shard_id]
    items[:] = selected
```

Run the test suite across 4 shards with Velda:

```bash
vbatch -N 4 --name test-suite pytest -q
```

Or run a single shard locally (for debugging):

```bash
VELDA_SHARD_ID=0 VELDA_TOTAL_SHARDS=4 pytest -q
```

## Homogeneous Shards

Homogeneous shards run identical worker processes that pull work from a shared job queue. The queue is responsible for distributing work; each worker is interchangeable.

### Start N workers (job queue)

Example: start N identical Python workers that pop jobs from Redis and process them.

`worker.py`:

```python
import os
import time
import json
import redis

REDIS_URL = os.environ.get('REDIS_URL', 'redis://localhost:6379/0')
r = redis.from_url(REDIS_URL)

def process_job(job):
    data = json.loads(job)
    # do the real work
    print('processing', data)

if __name__ == '__main__':
    while True:
        job = r.lpop('jobs')
        if not job:
            time.sleep(1)
            continue
        try:
            process_job(job)
        except Exception:
            # consider pushing failed jobs to a dead-letter queue
            raise
```

Start 8 workers with Velda:

```bash
vbatch -N 8 --name workers python worker.py
```

Each worker is identical and will claim the next job from the shared queue; this scales horizontally by increasing `-N`.
