# Configuring Resource Pools

Resource pools in Velda define pre-configured compute profiles that users can access with the `vrun` command. This guide explains how to configure and manage resource pools in your Velda cluster.

## Overview

Resource pools are the fundamental building blocks for compute capacity in Velda. Each pool represents a collection of worker agents that can execute workloads. Pools can be statically defined or dynamically auto-scaled based on demand.

## Configuration Methods

Depending on your deployment method, you have different options for configuring pools:

### Using Infrastructure-as-Code (Recommended)

If you deployed Velda using Terraform or CloudFormation, these modules typically provide higher-level abstractions for defining pools. **This is the recommended approach** as it:

- Integrates pool configuration with your infrastructure deployment
- Provides validation and type safety
- Manages the full lifecycle (creation, updates, deletion)
- Keeps configuration in version control

### Raw Configuration Strategies

For manual deployments or when you need direct control over the configuration, you can use these raw strategies:

#### 1. Direct Config File

Edit the Velda API server configuration file directly. The configuration is typically stored at `/etc/velda/config.yaml` or specified via the `--config` flag.

**Example configuration:**

```yaml
server:
  grpc_address: "0.0.0.0:50051"
  http_address: "0.0.0.0:8081"

storage:
  zfs:
    pool: zpool

agent_pools:
  - name: "cpu-pool"
    auto_scaler:
      backend:
        aws_launch_template:
          region: "us-west-2"
          launch_template_name: "velda-workers" # Template defines common parameters like SubNet/SecurityGroup.
          instance_type: "c5.2xlarge"
      max_agents: 20
      min_idle_agents: 1
      max_idle_agents: 5
      min_agents: 0
      default_slots_per_agent: 1
      
  - name: "gpu-pool"
    auto_scaler:
      backend:
        aws_launch_template:
          region: "us-west-2"
          launch_template_name: "velda-workers"
          instance_type: "p3.2xlarge"
      max_agents: 10
      min_idle_agents: 0
      max_idle_agents: 2

  # Pool without auto_scaler config needs to be started manually.
  - name: "static-pool"
```

After modifying the configuration file, restart the API server:

```bash
sudo systemctl restart velda-apiserver
```

#### 2. Using Pool Provisioners

Pool provisioners allow you to store pool configurations externally and have the API server sync them automatically. This is useful when:

- You want to manage pools independently from the API server deployment
- Multiple teams need to manage different pools
- You're using Terraform/IaC but want more flexibility

See the [Pool Provisioners](pool-provisioners.md) guide for detailed information.

#### 3. Dynamic Pool Creation

If `allow_new_pool` is enabled in the configuration, pools can be created dynamically at runtime. However, this is generally not recommended for production use.

```yaml
allow_new_pool: true
```

## Configuration File Structure

The complete configuration file uses Protocol Buffer message definitions. The main structure is:

```yaml
server:
  grpc_address: string
  http_address: string

storage:
  # Storage backend (zfs or mini)
  
agent_pools:
  - name: string
    auto_scaler:
      backend: # See Pool Backends reference
      # Autoscaling parameters - See Autoscaling guide
      
provisioners:
  # External pool configuration sources
  # See Pool Provisioners guide

# IP:Port to connect to apiserver. Most backends will inject this to the agent config. If not set, will auto-infer from IP address.
default_broker_info:
  address: string
```

## Next Steps

- **[Pool Backends Reference](pool-backends.md)**: Learn about all available backend types (AWS, GCP, Nebius, Mithril, DigitalOcean, Kubernetes)
- **[Autoscaling Configuration](pool-autoscaling.md)**: Understand autoscaling parameters and strategies
- **[Pool Provisioners](pool-provisioners.md)**: Configure external pool configuration stores
- **[Kubernetes CRD Integration](pool-kubernetes-crd.md)**: Use Kubernetes custom resources to manage pools

## Best Practices

1. **Start with IaC**: Use Terraform or CloudFormation modules when available
2. **Version Control**: Keep all pool configurations in version control
3. **Gradual Scaling**: Start with conservative `min_idle` and `max_agents` values
4. **Monitor Costs**: Set appropriate `max_agents` to control cloud costs
5. **Use Provisioners for Flexibility**: Separate pool management from API server deployment when needed
6. **Test Changes**: Validate configuration changes in a staging environment first
