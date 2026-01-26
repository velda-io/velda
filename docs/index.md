# What is Velda?

Velda is a dev-friendly workload orchestration platform designed for AI, ML and data-intensive tasks. It allows users to run commands on remote compute like local machine.

## The `vrun` & `vbatch` Command

A command prefix is all you need to run commands on remote compute resources. No Dockerfile, no Kubernetes manifests, and no context switching.

### Interactive execution with `vrun`
`vrun` gives you same experience as your local terminal, but with compute power of your cluster.

```bash
# Run locally (with CPU only)
python train.py

# Run with 8 GPUs on a remote resource pool
vrun -P gpu-a100-8 python train.py
```

You may add `vrun` to any command in your sub-process invocations, to leverage the additional power from your cloud. Depending on your cluster setup and the cloud capacity, the launch only takes a second for warm start and less than 30seconds for a cold start.

### Running batch jobs with `vbatch`
`vbatch` command is similar to `vrun`, but it runs the job in the background, so you can detach your terminal and continue monitor the tasks.

```bash
vbatch -f -P gpu-a100-8 python train.py  # -f streams logs immediately
```

Example usage:

* Start multiple workers with your preferred task orchestrator: `vbatch -N 10 python worker.py`
* Parallelism: [Run sharded jobs](user-guide/sharded-jobs.md) (e.g. tests, data processing)
* Pipelines: [Build sophisticated job pipelines](user-guide/dags-and-workflows.md)
* Distributed training: Support for ([Gang scheduling](user-guide/gang-scheduling.md))

## Features

### Zero-image overhead.
Traditional cloud scaling rely on container images, and this often result in over 10 minutes delay for each iterations. Velda skips this step and can launch job just in seconds.

### Dynamic Resource Allocation
Resources are provisioned on-demand and released immediately upon completion.

### Consistent environment
Use `pip`, `apt`, `conda` freely. Every user gets a persisted, isolated environment. Velda guarantees consistency across jobs by sharing underlying storage.

### Multi-Cloud Support
Velda can be deployed on most hyperscaler & newclouds (AWS, Google Cloud, Nebius) or on-premises infrastructure. The workload may run from clouds or network regions that is different from where the server is hosted (network cost and latency may apply).

## Use Cases

### Machine Learning & AI
- Train models with distributed GPU clusters
- Run hyperparameter tuning at scale
- Pipeline training with other steps including pre-processing & eval.

### Development environment
- Save cost on your dev-machine by only allocating GPUs when running GPU workloads
- Auto-shutdown machines when idle
- Onboard new developers with customized instance images

### Data Processing
- Process large datasets with distributed computing
- Run ETL pipelines across multiple nodes
- Execute data analysis workflows

### Software Development
- Compile large codebases with multi-core machines, or distribute compile commands remotely
- Run CI/CD pipelines on cloud infrastructure
- Test applications in production-like environments

## Learn more
Read [our blog](https://velda.io/blog/how-velda-works-accelerating-ml-development) for the underlying implementation.