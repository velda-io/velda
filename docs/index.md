# What is Velda?

Velda is a container orchestration platform designed for AI/ML workloads. It allows users to run commands on remote compute resources by prefixing them with `vrun`.

## The `vrun` Command

The `vrun` command executes workloads on remote compute resources. Any command can be prefixed with `vrun` to run it on configured resource pools.

```bash
# Run locally
python train.py

# Run with 4 GPUs on a remote resource pool
vrun -P gpu-4xa100 python train.py
```

No container image building or Kubernetes manifests are required.

## Features

### Dynamic Resource Allocation
Workloads can be assigned to different resource pools. Resources are allocated when jobs run and released when they complete.

### Environment Isolation
Each user get isolated and persisted environment. Users can install packages using standard package managers (pip, apt, npm, conda) without affecting other users.

### Multi-Cloud Support
Velda can be deployed on AWS, Google Cloud, or on-premises infrastructure. The agent may run from other clouds or network regions(egress and latency may apply).

## Use Cases

### Machine Learning & AI
- Train models with distributed GPU clusters
- Run hyperparameter tuning at scale
- Deploy inference services with auto-scaling

### Data Processing
- Process large datasets with distributed computing
- Run ETL pipelines across multiple nodes
- Execute data analysis workflows

### Software Development
- Compile large codebases with multi-core machines
- Run CI/CD pipelines on cloud infrastructure
- Test applications in production-like environments

## Learn more
Read [our blog](https://velda.io/blog/how-velda-works-accelerating-ml-development) for the underlying implementation.