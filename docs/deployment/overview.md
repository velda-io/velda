# Deploying Velda

This section covers deploying and managing self-hosted Velda clusters.

## Deployment Options

Velda supports several deployment methods:

### 1. Terraform Deployment

Infrastructure-as-code deployment:
- Automated deployment with major cloud providers
- Reproducible & updatable infrastructure configuration
- Suitable for production workloads

Check out [Terraform templates](https://github.com/velda-io/velda-terraform/) to get started.

### 2. Marketplace (Coming soon)

We're working with most cloud providers to provide marketplace options for quick setup.

### 3. Velda Cloud
Managed service option that is ready to run:
- Hosted infrastructure
- GPU and CPU resources available
- Includes free tier with monthly credits
- Suitable for individuals and small teams, or exploration.

[Get Started with Velda Cloud](https://velda.cloud/)

### 4. Manual Cluster Setup (Custom Deployments)

Direct installation on Linux machines:
- Install on bare metal or VMs
- Full control over all components
- Customizable configuration
- Suitable for on-premises deployments

[Manual Setup Guide](manual-setup.md)

## Architecture Overview

A Velda cluster consists of:

- **API Server**: Control plane that manages scheduling, authentication, and orchestration
- **Agents**: Worker nodes that execute containers and report resource status
- **Storage**: ZFS-based storage shared via NFS for user data and instances. Typically attached with controller.
-- **SSH client**: Use a standard SSH client to connect to cluster nodes

```
                         ┌────────────────────────────────────┐
                         │            Velda Cluster           │
                         │                                    │
                         │  ┌─────────────┐   ┌─────────────┐  │
                         │  │ API Server  │◄──┤ Agent Pool  │  │
                         │  │  (Control)  │   │  (Workers)  │  │
                         │  └─────────────┘   └─────────────┘  │
                         │         │           ▲                │
                         │         │           │                │
                         │  ┌─────────────┐    │                │
                         │  │  Storage    │    │                │
                         │  │ (ZFS + NFS) │    │                │
                         │  └─────────────┘    │                │
                         └────────────────────────────────────┘
                                   ▲
                                   │ SSH
                            ┌─────────────┐
                            │ SSH client  │
                            └─────────────┘
```

## Choosing a Deployment Method

| Deployment Method | Use Case | Setup Time | Scalability | Management |
|-------------------|----------|------------|-------------|------------|
| **Velda Cloud** | Getting started, small teams | < 5 min | Auto-scaling | Managed |
| **Terraform** | Production, repeatable infra | 15-30 min | Highly scalable | IaC managed |
| **Manual Setup** | Custom deployments, on-prem | 30-60 min | Configurable | Self-managed |

## System Requirements

### For Self-Hosted Deployments

**API Server (Control Plane):**
- Linux (Ubuntu 22.04+ recommended)
- 2+ vCPUs, 4+ GB RAM
- Dedicated disk for ZFS storage
- Network accessible by all agents and clients

**Agent Nodes (Workers):**
- Linux (Ubuntu 22.04+ recommended)
- AMD64 or ARM64 architecture
- 1+ vCPUs, 4+ GB RAM (more for GPU workloads)
- CGroup v2 enabled (CGroup v1 disabled)
- Direct IP access to API server

**For GPU Workloads:**
- NVIDIA GPU drivers

