# Mithril.ai Backend for Velda

This package provides integration between Velda and [Mithril.ai](https://mithril.ai), enabling dynamic GPU resource provisioning through Mithril's spot bidding marketplace.

## Overview

Mithril is an AI-compute omnicloud that provides GPU resources through a two-sided auction system. This backend allows Velda to:

- Automatically provision GPU instances based on workload demand
- Leverage spot pricing for cost-effective compute
- Scale from 1 to thousands of GPUs dynamically
- Support multiple instance types (A100, H100, etc.)

## Features

- **Spot Bid Management**: Automatically creates and manages spot bids for GPU instances
- **Multi-Pool Support**: Configure multiple pools with different instance types and price limits
- **Auto-Provisioning**: Dynamically create agent pools based on configuration
- **Cost Control**: Set limit prices per GPU-hour to control costs
- **Preemption Handling**: Gracefully handles spot instance preemption
- **Bid Pooling**: Pause unused bids instead of terminating them for faster restart times
- **Prefix-based Filtering**: Uses configuration hash to identify and manage related bids
- **Tailscale Integration**: Optional automatic Tailscale network setup

## Configuration

### Prerequisites

1. **Mithril Account**: Sign up at [mithril.ai](https://mithril.ai)
2. **API Key**: Generate an API key from [Mithril console](https://app.mlfoundry.com/account/apikeys) (format: `fkey_...`)
3. **SSH Keys**: Configure SSH keys in your Mithril project
4. **Project ID**: Note your Mithril project ID (format: `proj_...`)

### Auto-Provisioner Configuration

The recommended approach is to use the `MithrilAutoProvisioner`:

```yaml
provisioners:
  - mithril_auto:
      project_id: "proj_abc123456"
      region: "us-central1-a"
      api_token: "${MITHRIL_API_KEY}"
      
      ssh_key_ids:
        - "sshkey_1234567890"
      
      labels:
        environment: "production"
        managed-by: "velda"
      
      autoscaler_config:
        max_agents: 10
        min_agents: 0
        max_idle_agents: 1
        idle_decay: "5m"
      
      pool_details:
        - pool_name: "h100-pool"
          instance_type: "it_h100_8x"
          limit_price_per_gpu_hour: 3.5
          description: "8x H100 GPUs for training"
        
        - pool_name: "a100-pool"
          instance_type: "it_a100_8x"
          limit_price_per_gpu_hour: 2.0
          description: "8x A100 GPUs for general workloads"
```

### Direct Pool Configuration

Alternatively, configure pools directly:

```yaml
agent_pools:
  - name: "custom-h100"
    auto_scaler:
      max_agents: 10
      backend:
        mithril_spot_bid:
          instance_type: "it_h100_8x"
          region: "us-central1-a"
          limit_price_per_gpu_hour: 3.5
          project_id: "proj_abc123456"
          api_token: "${MITHRIL_API_KEY}"
          ssh_key_ids:
            - "sshkey_1234567890"
```

## Configuration Fields

### MithrilAutoProvisioner

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `project_id` | string | Yes | Your Mithril project ID (format: `proj_...`) |
| `region` | string | Yes | Region to launch instances (e.g., "us-central1-a") |
| `api_token` | string | Yes | Mithril API key (format: `fkey_...`) |
| `ssh_key_ids` | []string | No | SSH key IDs (format: `sshkey_...`) configured in Mithril |
| `labels` | map | No | Labels applied to all instances |
| `agent_version_override` | string | No | Specific Velda agent version to install |
| `autoscaler_config` | AutoScaler | No | Default autoscaler config for all pools |
| `api_endpoint` | string | No | API endpoint override (default: https://api.mithril.ai) |
| `pool_details` | []PoolDetail | Yes | List of pool configurations |

### PoolDetail

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `pool_name` | string | Yes | Unique name for the pool |
| `instance_type` | string | Yes | Mithril instance type ID (format: `it_...`) |
| `limit_price_per_gpu_hour` | float | Yes | Maximum price per GPU-hour in USD |
| `description` | string | No | Human-readable description |
| `autoscaler_config` | AutoScaler | No | Pool-specific autoscaler override |

### AutoscalerBackendMithrilSpotBid

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `instance_type` | string | Yes | Mithril instance type ID (format: `it_...`) |
| `region` | string | Yes | Region for instances (e.g., "us-central1-a") |
| `limit_price_per_gpu_hour` | float | Yes | Maximum price per GPU-hour |
| `project_id` | string | Yes | Mithril project ID (format: `proj_...`) |
| `api_token` | string | Yes | API key (format: `fkey_...`) |
| `ssh_key_ids` | []string | No | SSH key IDs (format: `sshkey_...`) |
| `labels` | map | No | Instance labels/tags (not directly supported in API) |
| `agent_config` | AgentConfig | No | Velda agent configuration |
| `agent_version_override` | string | No | Specific agent version |
| `startup_script` | string | No | Custom cloud-init script |
| `api_endpoint` | string | No | API endpoint override (default: https://api.mithril.ai) |
| `max_suspended_bids` | int32 | No | Max paused bids to keep for reuse (default: 0) |
| `tailscale_config` | TailscaleConfig | No | Tailscale configuration (pre_auth_key, server) |

## Bid Pooling and Prefix-based Filtering

### Bid Pooling

The backend supports **bid pooling** to reduce startup times and costs. When `max_suspended_bids` is set to a value greater than 0:

1. **Scale Down**: Instead of terminating bids, they are paused using the Mithril PATCH API
2. **Scale Up**: Paused bids are resumed before creating new ones
3. **Pool Management**: Excess paused bids beyond the limit are automatically terminated

This significantly reduces the time to provision new instances since paused bids can be resumed almost instantly.

**Example Configuration:**
```yaml
mithril_spot_bid:
  max_suspended_bids: 5  # Keep up to 5 paused bids for reuse
```

### Prefix-based Filtering

Each pool generates a unique prefix hash based on its configuration parameters:
- Instance type
- Region
- Project ID
- Limit price
- SSH keys
- Agent version

This ensures that:
- Only bids created with compatible configurations are reused
- Multiple pools with different configurations don't interfere with each other
- Bids are easily identifiable in the Mithril console

The prefix format is `velda-{hash[:8]}-{random}`, e.g., `velda-a1b2c3d4-x7y9z`

## Tailscale Integration

The Mithril backend supports automatic Tailscale network setup for secure connectivity to your instances.

### Configuration

Add the `tailscale_config` parameter to your backend configuration:

```yaml
autoscaler:
  backend:
    mithril_spot_bid:
      tailscale_config:
        pre_auth_key: "tskey-auth-..."
      # other config...
```

### How It Works

When a Tailscale auth key is provided:
1. Tailscale is installed on instance startup
2. The instance authenticates using your auth key
3. Tailscale SSH is automatically enabled
4. The instance joins your Tailscale network

### Getting an Auth Key

1. Visit the [Tailscale Admin Console](https://login.tailscale.com/admin/settings/keys)
2. Generate a new auth key
3. For dynamic compute instances, consider using:
   - **Reusable keys**: Can be used for multiple instances
   - **Ephemeral keys**: Auto-cleanup when instances are removed
   - **Tagged keys**: Apply ACL tags automatically

### Benefits

- **Secure Access**: Direct SSH access to instances without exposing public IPs
- **Service Mesh**: Connect instances to your private network
- **No Firewall Config**: Works through NAT and firewalls
- **Automatic DNS**: Instances get MagicDNS names

## Instance Types

Mithril supports various GPU instance types. Instance type IDs follow the format `it_...` and can be retrieved via the API:

```bash
curl -H "Authorization: Bearer fkey_your_key" \
     https://api.mithril.ai/v2/instance-types
```

Common instance types include:
- H100 configurations: `it_h100_8x`, `it_h100_single`
- A100 configurations: `it_a100_8x`, `it_a100_single`
- Other GPU types available per region

Refer to the [Mithril API documentation](https://docs.mithril.ai/compute-api/compute-api-reference/instance-types) for the full list.

## Pricing and Spot Bids

### Limit Prices

Set `limit_price_per_gpu_hour` to control maximum costs:
- Your bid is allocated when the spot price is below your limit
- You're billed at the current spot price (≤ your limit)
- Instances are preempted if spot price exceeds your limit

### Example Pricing Strategy

```yaml
pool_details:
  # Premium pool - always available
  - pool_name: "h100-premium"
    instance_type: "it_h100_8x"
    limit_price_per_gpu_hour: 5.0  # High limit for availability
  
  # Cost-optimized pool
  - pool_name: "h100-budget"
    instance_type: "it_h100_8x"
    limit_price_per_gpu_hour: 2.0  # Lower limit for cost savings
```

## Autoscaling Behavior

The backend automatically:
1. **Scale Up**: 
   - First tries to resume a paused bid from the pool (if available)
   - Creates new spot bids when agents are needed and no paused bids exist
2. **Scale Down**: 
   - Pauses bids when agents are idle (if within `max_suspended_bids` limit)
   - Terminates bids when pool is full or pooling is disabled
3. **Preemption Recovery**: Automatically recreates bids if instances are preempted
4. **Cost Optimization**: Uses spot pricing and bid pooling to minimize costs
5. **Pool Scanning**: Periodically scans for paused bids to populate the pool

## Security Best Practices

1. **API Token**: Store API key in environment variable
   ```bash
   export MITHRIL_API_KEY="fkey_your_key_here"
   ```

2. **SSH Keys**: Use separate keys for each environment
3. **Labels**: Tag instances for tracking and cost allocation
4. **Limit Prices**: Set conservative limits to prevent cost overruns

## Testing

Run the test suite with:

```bash
MITHRIL_API_KEY="fkey_your_key" \
MITHRIL_PROJECT_ID="proj_abc123456" \
go test --tags mithril -v ./pkg/broker/backends/mithril
```

## Limitations

1. **API v2**: Uses Mithril's REST API v2 (https://api.mithril.ai/v2)
2. **Preemption**: Spot instances can be preempted with 5-minute notice
3. **Quota Limits**: Subject to Mithril project quotas
4. **Region Availability**: Instance types vary by region
5. **Labels**: Labels are not directly supported in bid responses from the API

## Troubleshooting

### Bids Not Allocated

- Check if your limit price is competitive for the current market
- Verify region has available capacity
- Ensure SSH keys are properly configured
- Check Mithril quota limits

### Authentication Errors

- Verify `MITHRIL_API_KEY` is set correctly (should start with `fkey_`)
- Ensure API key has necessary permissions
- Check `project_id` matches your Mithril project (should start with `proj_`)

### Instance Connection Issues

- Verify SSH keys are added to your Mithril project
- Check security group/firewall rules
- Ensure startup script completes successfully

## Support

For Mithril-specific issues:
- Documentation: https://docs.mithril.ai
- Support: support@mithril.ai

For Velda integration issues:
- File an issue in the Velda repository
- Include relevant logs and configuration

## References

- [Mithril Compute API Documentation](https://docs.mithril.ai/compute-api)
- [API Overview and Quickstart](https://docs.mithril.ai/compute-api/api-overview-and-quickstart)
- [Spot API Reference](https://docs.mithril.ai/compute-api/compute-api-reference/spot)
- [Spot Auction Mechanics](https://docs.mithril.ai/compute-and-storage/spot-bids/spot-auction-mechanics)
- [Flow CLI](https://github.com/mithrilcompute/flow)
- [Instance Types API](https://docs.mithril.ai/compute-api/compute-api-reference/instance-types)
