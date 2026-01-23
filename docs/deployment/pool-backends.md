# Pool Backend References

Pool backends define how Velda provisions and manages worker agents. Each backend integrates with a different infrastructure provider or orchestration system.

## Available Backends

Velda supports the following backend types:

| Backend | Provider | Use Case |
|---------|----------|----------|
| **AWS Launch Template** | Amazon Web Services | EC2 instances with custom launch templates |
| **GCE Instance Group** | Google Cloud Platform | Managed instance groups on GCP |
| **Nebius Launch Template** | Nebius Cloud | GPU instances on Nebius infrastructure |
| **Mithril Spot Bid** | Mithril AI | Spot GPU instances with competitive pricing |
| **DigitalOcean Droplet** | DigitalOcean | Standard and GPU droplets |
| **Kubernetes** | Kubernetes | Pods in a Kubernetes cluster |
| **Command** | Custom | Custom scripts for any provider |

## Backend Configuration Reference

For complete protocol buffer definitions, see:

- [config.proto](https://github.com/velda-io/velda/main/proto/config/config.proto) - Main pool configuration
- [agent/config.proto](https://github.com/velda-io/velda/main/proto/agent/config.proto) - Agent configuration

---

## AWS Launch Template

Provisions EC2 instances using AWS Launch Templates. Supports instance reuse and flexible configuration.

**Proto Definition:** `AutoscalerBackendAWSLaunchTemplate`

### Configuration Fields

```protobuf
message AutoscalerBackendAWSLaunchTemplate {
  string region = 1;                          // AWS region (e.g., "us-west-2")
  string launch_template_name = 2;            // Launch template name
  string instance_name_prefix = 3;            // Prefix for instance names
  bool use_instance_id_as_name = 4;           // Use instance ID instead of Name tag
  string instance_type = 5;                   // Override instance type (e.g., "p3.2xlarge")
  string agent_config_content = 6;            // Agent config as string (deprecated)
  velda.agent.AgentConfig agent_config = 12;  // Structured agent config
  string zone = 7;                            // Availability zone
  string subnet_id = 8;                       // Subnet ID
  map<string, string> tags = 9;               // Additional instance tags
  string ami_id = 10;                         // AMI ID to use
  repeated string security_group_ids = 11;    // Security group IDs
  int32 max_stopped_instances = 13;           // Keep stopped instances for reuse
  google.protobuf.Duration max_instance_lifetime = 14; // Max lifetime for reuse
}
```

### Example Configuration

```yaml
agent_pools:
  - name: "gpu-pool-aws"
    auto_scaler:
      backend:
        aws_launch_template:
          region: "us-west-2"
          launch_template_name: "velda-gpu-workers"
          instance_type: "p3.2xlarge"
          instance_name_prefix: "velda-gpu"
          ami_id: "ami-0123456789abcdef0"
          subnet_id: "subnet-abc123"
          security_group_ids:
            - "sg-xyz789"
          tags:
            Environment: "production"
            ManagedBy: "velda"
          agent_config:
            sandbox_config:
              max_time: "12h"
      max_agents: 10
      min_idle_agents: 1
```


---

## GCE Instance Group

Provisions Google Compute Engine instances using managed instance groups.

**Proto Definition:** `AutoscalerBackendGCEInstanceGroup`

### Configuration Fields

```protobuf
message AutoscalerBackendGCEInstanceGroup {
  string project = 1;              // GCP project ID
  string zone = 2;                 // GCP zone (e.g., "us-central1-a")
  string instance_group = 3;       // Instance group name
  string instance_name_prefix = 4; // Instance name prefix
}
```

### Example Configuration

```yaml
agent_pools:
  - name: "gpu-pool-gcp"
    auto_scaler:
      backend:
        gce_instance_group:
          project: "my-gcp-project"
          zone: "us-central1-a"
          instance_group: "velda-gpu-workers"
          instance_name_prefix: "velda-gpu"
      max_agents: 20
      min_idle_agents: 2
```

---

## Nebius Launch Template

Provisions GPU instances on Nebius Cloud infrastructure with support for high-performance GPUs like H200.

**Proto Definition:** `AutoscalerBackendNebiusLaunchTemplate`

### Configuration Fields

```protobuf
message AutoscalerBackendNebiusLaunchTemplate {
  string parent_id = 1;                       // Project or folder ID
  string instance_name_prefix = 2;            // Instance name prefix
  string platform = 3;                        // Platform type (e.g., "gpu-h200-hxm")
  string resource_preset = 4;                 // Resource preset (e.g., "1gpu-16vcpu-200gb")
  string agent_config_content = 5;            // Agent config string (deprecated)
  velda.agent.AgentConfig agent_config = 6;   // Structured agent config
  string subnet_id = 7;                       // Subnet ID
  int64 boot_disk_size_gb = 8;                // Boot disk size (default: 40)
  bool preemptible = 9;                       // Use preemptible instances
  bool use_public_ip = 10;                    // Assign public IP
  map<string, string> labels = 11;            // Instance labels
  string admin_ssh_key = 12;                  // SSH public key
  string agent_version_override = 13;         // Override agent version
  int32 max_disk_pool_size = 14;              // Max disks to pool for reuse
  TailscaleConfig tailscale_config = 15;      // Tailscale configuration
}
```

### Example Configuration

```yaml
agent_pools:
  - name: "h200-pool"
    auto_scaler:
      backend:
        nebius_launch_template:
          parent_id: "project-abc123"
          instance_name_prefix: "velda-h200"
          platform: "gpu-h200-hxm"
          resource_preset: "1gpu-16vcpu-200gb"
          subnet_id: "subnet-xyz"
          boot_disk_size_gb: 100
          preemptible: false
          use_public_ip: true
          admin_ssh_key: "ssh-rsa AAAAB3..."
          max_disk_pool_size: 5
          tailscale_config:
            server: "https://controlplane.tailscale.com"
            pre_auth_key: "tskey-auth-..."
      max_agents: 8
      min_idle_agents: 0
      max_idle_agents: 1
```

---

## Mithril Spot Bid

Provisions GPU instances using Mithril AI's spot market for competitive pricing.

**Proto Definition:** `AutoscalerBackendMithrilSpotBid`

### Configuration Fields

```protobuf
message AutoscalerBackendMithrilSpotBid {
  string instance_type = 1;                   // Instance type (e.g., "8xh100")
  string region = 2;                          // Region (e.g., "us-west-2")
  double limit_price = 3;                     // Max price per GPU-hour
  string agent_config_content = 4;            // Agent config string (deprecated)
  velda.agent.AgentConfig agent_config = 5;   // Structured agent config
  map<string, string> labels = 6;             // Instance labels
  repeated string ssh_key_ids = 7;            // SSH key IDs
  string agent_version_override = 8;          // Override agent version
  string startup_script = 9;                  // Custom startup script
  string project_id = 10;                     // Mithril project ID
  string api_token = 11;                      // API token (use env var)
  string api_endpoint = 12;                   // API endpoint override
  int32 max_suspended_bids = 13;              // Max suspended bids for reuse
  TailscaleConfig tailscale_config = 14;      // Tailscale configuration
  int32 memory_gb = 15;                       // Memory in GB
}
```

### Example Configuration

```yaml
agent_pools:
  - name: "mithril-h100"
    auto_scaler:
      backend:
        mithril_spot_bid:
          instance_type: "8xh100"
          region: "us-west-2"
          limit_price: 4.50
          project_id: "proj_abc123"
          api_token: "${MITHRIL_API_TOKEN}"  # Set via environment
          ssh_key_ids:
            - "key_xyz789"
          memory_gb: 640
          max_suspended_bids: 2
      max_agents: 5
      min_idle_agents: 0
```

---

## DigitalOcean Droplet

Provisions DigitalOcean droplets, including GPU instances for AMD ROCm workloads.

**Proto Definition:** `AutoscalerBackendDigitalOceanDroplet`

### Configuration Fields

```protobuf
message AutoscalerBackendDigitalOceanDroplet {
  string name = 1;                            // Droplet name
  string region = 2;                          // Region (e.g., "atl1", "nyc1")
  string size = 3;                            // Size/tier (e.g., "gpu-mi300x1-192gb-devcloud")
  string image = 4;                           // Image (e.g., "amd-rocm71software")
  repeated int32 ssh_key_ids = 5;             // SSH key IDs
  bool backups = 6;                           // Enable backups
  bool ipv6 = 7;                              // Enable IPv6
  bool monitoring = 8;                        // Enable monitoring
  repeated string tags = 9;                   // Droplet tags
  string vpc_uuid = 10;                       // VPC UUID
  string agent_config_content = 11;           // Agent config string (deprecated)
  velda.agent.AgentConfig agent_config = 12;  // Structured agent config
  map<string, string> labels = 13;            // Additional labels
  string agent_version_override = 14;         // Override agent version
  string api_token = 15;                      // API token (use env var)
  string api_endpoint = 16;                   // API endpoint override
  TailscaleConfig tailscale_config = 17;      // Tailscale configuration
}
```

### Example Configuration

```yaml
agent_pools:
  - name: "do-rocm-pool"
    auto_scaler:
      backend:
        digitalocean_droplet:
          region: "atl1"
          size: "gpu-mi300x1-192gb-devcloud"
          image: "amd-rocm71software"
          ssh_key_ids:
            - 12345678
          monitoring: true
          api_token: "${DIGITALOCEAN_API_TOKEN}"
      max_agents: 10
      min_idle_agents: 1
```

---

## Kubernetes

Provisions pods in a Kubernetes cluster using pod templates.

See [Kubernetes CRD Integration](pool-kubernetes-crd.md) for more advanced Kubernetes integration.

---

## Command Backend

Execute custom shell commands for lifecycle management. Useful for integrating with custom provisioning systems.

**Proto Definition:** `AutoscalerBackendCommand`

### Configuration Fields

```protobuf
message AutoscalerBackendCommand {
  string start = 1;       // Command to start a worker
  string stop = 2;        // Command to stop a worker
  string list = 3;        // Command to list workers
  string batch_start = 4; // Command for batch starts
}
```

### Example Configuration

```yaml
agent_pools:
  - name: "custom-pool"
    auto_scaler:
      backend:
        command:
          start: "/usr/local/bin/velda-provision-worker.sh start"
          stop: "/usr/local/bin/velda-provision-worker.sh stop ${WORKER_NAME}"
          list: "/usr/local/bin/velda-provision-worker.sh list"
      max_agents: 10
      min_idle_agents: 2
```

The commands should:
- **start**: Output the worker name to stdout
- **stop**: Accept worker name as argument or via environment variable
- **list**: Output worker names (one per line) and their status

---

## Tailscale Configuration

Velda requires all agents to connect with the same VPC as the storage server.

For multi-cloud setup, you may use wireguard or tailscale to connect them to the VPC of your apiserver.

Velda embed tail-scale support with several backends, so they can join the tail-net when the worker starts.

```protobuf
message TailscaleConfig {
  string server = 1;        // Control server URL
  string pre_auth_key = 2;  // Pre-authentication key
}
```

### Example

```yaml
tailscale_config:
  server: "https://controlplane.tailscale.com"
  pre_auth_key: "tskey-auth-kABCDEF123..."
```
