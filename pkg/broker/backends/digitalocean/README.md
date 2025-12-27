# DigitalOcean Droplet Backend

This backend provides automatic provisioning of Velda agents on DigitalOcean droplets, with special support for AMD GPU instances via the AMD DigitalOcean API.

## Features

- Automatic droplet creation and deletion
- Support for AMD MI300X GPU instances
- Custom image and size selection
- SSH key management
- Tagging and labeling
- VPC networking support
- Tailscale integration
- Multi-pool configuration

## Configuration

### Backend Configuration

The `AutoscalerBackendDigitalOceanDroplet` message configures individual pools:

```yaml
autoscaler:
  backend:
    digitalocean_droplet:
      # Required fields
      size: "gpu-mi300x1-192gb-devcloud"  # Droplet size/tier
      region: "atl1"                       # Region slug
      image: "amd-rocm71software"          # Image slug or ID
      
      # SSH keys (required for access)
      ssh_key_ids: [52896465]
      
      # Optional fields
      api_token: "DIGITALOCEAN_API_TOKEN"  # Or set via env var
      api_endpoint: "https://api-amd.digitalocean.com"  # For AMD instances
      backups: false
      ipv6: false
      monitoring: true
      vpc_uuid: ""
      
      # Agent configuration
      agent_version_override: "1.0.0"
      labels:
        environment: "production"
      
      # Tailscale (optional)
      tailscale_config:
        server: "https://login.tailscale.com"
        pre_auth_key: "tskey-auth-..."
```

### Auto-Provisioner Configuration

The `DigitalOceanAutoProvisioner` creates multiple pools:

```yaml
provisioners:
  - digitalocean_auto:
      region: "atl1"
      api_token: "DIGITALOCEAN_API_TOKEN"
      ssh_key_ids: [52896465]
      
      pool_details:
        - pool_name: "amd-mi300x"
          size: "gpu-mi300x1-192gb-devcloud"
          image: "amd-rocm71software"
          autoscaler_config:
            max_agents: 10
            min_agents: 0
```

See [example-config.yaml](example-config.yaml) for a complete example.

## API Endpoints

- **Standard DigitalOcean API**: `https://api.digitalocean.com`
- **AMD DigitalOcean API**: `https://api-amd.digitalocean.com`

The AMD API provides access to GPU instances like the MI300X.

## Authentication

Set your API token via:
1. Environment variable: `DIGITALOCEAN_API_TOKEN=dop_v1_...`
2. Configuration file: `api_token: "dop_v1_..."`

Get your token from: https://cloud.digitalocean.com/account/api/tokens

## SSH Keys

SSH keys must be pre-configured in your DigitalOcean account. Get the key IDs from:
https://cloud.digitalocean.com/account/security

## Example API Request

The backend makes requests similar to this:

```bash
curl -X POST -H 'Content-Type: application/json' \
    -H 'Authorization: Bearer '$TOKEN'' \
    -d '{"name":"velda-abc123",
        "region":"atl1",
        "size":"gpu-mi300x1-192gb-devcloud",
        "image":"amd-rocm71software",
        "ssh_keys":[52896465],
        "backups":false,
        "ipv6":false,
        "monitoring":false,
        "tags":["velda"],
        "user_data":"#!/bin/bash\n..."}' \
    https://api-amd.digitalocean.com/v2/droplets
```

## Available Sizes

Common GPU sizes on AMD DigitalOcean:
- `gpu-mi300x1-192gb-devcloud` - Single AMD MI300X GPU with 192GB

Standard CPU sizes:
- `s-1vcpu-1gb` - 1 vCPU, 1GB RAM
- `s-2vcpu-2gb` - 2 vCPU, 2GB RAM
- `s-2vcpu-4gb` - 2 vCPU, 4GB RAM
- `c-2` - 2 dedicated vCPU, 4GB RAM
- `g-2vcpu-8gb` - General purpose

See full list: https://slugs.do-api.dev/

## Available Regions

Common regions:
- `atl1` - Atlanta
- `nyc1`, `nyc3` - New York
- `sfo1`, `sfo2`, `sfo3` - San Francisco
- `ams3` - Amsterdam
- `sgp1` - Singapore
- `lon1` - London
- `fra1` - Frankfurt
- `tor1` - Toronto
- `blr1` - Bangalore

## Testing

Run the backend tests:

```bash
export DIGITALOCEAN_API_TOKEN="dop_v1_..."
export DIGITALOCEAN_SSH_KEY_ID="52896465"
go test --tags digitalocean -v ./pkg/broker/backends/digitalocean
```

## How It Works

1. **Scale Up**: Creates a new droplet with user-data script that:
   - Installs dependencies (curl, nfs-common)
   - Creates `/etc/velda/agent.yaml` with pool configuration
   - Initializes NVIDIA drivers (if GPU instance)
   - Optionally installs and configures Tailscale
   - Downloads and installs Velda agent
   - Starts the agent as a systemd service

2. **List Workers**: Queries DigitalOcean API for droplets with the `velda` tag and matching name prefix

3. **Scale Down**: Deletes the droplet by ID

## Limitations

- Droplets are identified by name prefix (hash of configuration)
- No support for droplet pooling/pausing (unlike Mithril backend)
- SSH keys must be pre-configured in DigitalOcean account
- API rate limits apply

## Troubleshooting

### Droplet creation fails
- Check API token is valid
- Verify SSH key IDs exist in your account
- Ensure region supports the requested size
- Check API endpoint (use AMD endpoint for GPU instances)

### Agent doesn't connect
- Verify `default_broker_info.address` is accessible from droplet
- Check security group/firewall rules
- Review droplet console logs
- Verify user-data script executed correctly

### Authentication errors
- Ensure `DIGITALOCEAN_API_TOKEN` environment variable is set
- Token should start with `dop_v1_`
- Generate new token if expired
