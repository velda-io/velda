# Mini-Velda

Mini-velda is a lightweight development sandbox that brings the full power of Velda directly from your local machine. It provides seamless scaling from your local development environment to cloud resources without any complex setup.

## Summary

Mini-velda creates a complete development environment with powerful scaling capabilities:

- **Local Development**: Full Linux development environment with your favorite tools and IDEs
- **Instant Scaling**: Use `vrun` to scale any workload to cloud resources (AWS, GCP) in seconds  
- **Consistent Environment**: Your code, data, and dependencies are automatically available on any machine
- **Zero Setup Scaling**: No container images to build or Kubernetes clusters to manage
- **Cost Efficient**: Only pay for cloud resources when actively running workloads

The sandbox environment is isolated and fully contained within a specified directory, making it easy to manage multiple development environments or clean up when no longer needed.

## Prerequisites

Before using mini-velda, ensure you have the following installed:

- **Docker**: For the local development environment
- **exportfs**: NFS server (install with `apt install nfs-kernel-server` on Ubuntu)

## Usage

### Initialize a Mini-Velda Sandbox

Create and start a new mini-velda development environment:

```bash
velda mini init ~/velda-sandbox
```

**Available flags:**
- `--base-image` (default: `ubuntu:24.04`): Docker image to use for the sandbox
- `--backends` (default: `["all"]`): Cloud backends to configure. Currently only AWS supports automatic configuration, but you may edit the config file manually.

**What happens during initialization:**
1. **Environment Setup**: Creates your development sandbox with a complete Linux environment
2. **Cloud Integration**: Interactive configuration for scaling to AWS, GCP, or other providers
3. **SSH Access**: Generates secure SSH keys and configures automatic access
4. **Development Tools**: Sets up Velda commands (`vrun`, `vbatch`) and user environment
5. **Ready to Scale**: Your environment is immediately ready to scale workloads to the cloud

### Connect and Scale Your Workloads

Once initialized, you can start developing and scaling immediately:

**Connect to your development environment (Run from your host):**
```bash
ssh mini-velda                 # SSH directly to your environment
velda run                      # Start interactive shell session
velda run -u root              # Run as root user
velda run -it bash             # Start specific shell
```

**Scale workloads to the cloud with `vrun`:**

```bash
# Run a Python script on cloud GPU
vrun -P aws:g4dn.xlarge python train_model.py

# Run batch processing on high-memory instances  
vrun -P aws:m6.16xlarge python process_data.py
```

**Run local development:**
```bash
# Code and test locally in your sandbox
python train_model.py          # Local development
git commit -m "working model"  # All your tools available

# Then scale the same code to cloud resources  
vrun -P aws:g4dn.xlarge python train_model.py  # Exact same environment, cloud GPU
```

### Start an Existing Sandbox

If you have a previously initialized sandbox, restart it with:

```bash
velda mini up ~/velda-sandbox
```

### Stop a Running Sandbox

Shut down the mini-velda environment:

```bash
velda mini down
```

**IDE Integration:**
- **VS Code/Cursor/Windsurf**: Connect via SSH to `mini-velda`
- **Terminal Access**: Direct SSH access with your keys
- **Port Forwarding**: Access services running in your sandbox

## Configuration

### Cloud Backend Integration

Mini-velda's power comes from seamless cloud scaling. During initialization, configure your cloud providers:

**AWS Integration:**
- Onboarding wizard that guide you on the necessary settings.
- Automatically infer default settings from your environment.

**GCP/k8s/other Integration:**
- Currently needs to manually update config.
- Config file is at `<sandbox-dir>/service.yaml`, and definition is [here](/proto/config/config.proto)

### Development Environment

Your mini-velda sandbox includes:
- Complete Ubuntu environment with sudo access
- Your SSH keys for secure access
- Pre-installed Velda tools (`vrun`, `vbatch`, `velda`)
- Persistent home directory and customizations
- All standard development tools and package managers

## Key Features

### Instant Cloud Scaling
- **Zero Setup**: No container images to build or Kubernetes to manage
- **Any Workload**: Machine learning, batch processing, microservices, HPC
- **Consistent Environment**: Your exact development environment on any cloud machine
- **Cost Efficient**: Pay only when running workloads, not for idle resources

### Development Experience  
- **IDE Integration**: Works with VS Code, Cursor, Windsurf, and any SSH-compatible editor
- **Persistent Environment**: All customizations, tools, and data are preserved and stored locally on your host.
- **No Context Switching**: Develop locally, scale instantly without changing your workflow

## Limitations

### Network Requirements

- **Direct Cloud Connectivity**: Your local machine must maintain network connectivity to cloud resources while workloads are running
  - VPN connection to your cloud provider's network, or
  - Direct network peering/transit gateway connectivity
  - Stable connection required for the duration of cloud workloads
  - Permission to start/configure/terminate compute nodes
    - Please be aware that authentication with expiration (e.g. most AWS SSO) may prevent the terminating of nodes.

### System Requirements

- **Local Environment**: Runs on Linux/macOS with Docker support
- **Resource Usage**: Minimal local resource usage - actual workloads run in the cloud
- **Port Usage**: Reserves a few local ports for API and SSH access

### Usage Limitations

- **Development Focus**: Optimized for development and experimentation, not production deployment
- **Single User**: Each sandbox is designed for individual developer use
- **Cloud Permissions**: Requires appropriate IAM permissions for launching cloud instances

## Troubleshooting

### Common Issues

**Connection Problems:**
```bash
# Test SSH access
ssh mini-velda

# Check if services are running
velda mini up ~/velda-sandbox

# Check logs of apiserver
less ~/velda-sandbox/apiserver.log
# Check logs of local agent
docker logs mini-velda-agent
```

**Cloud Scaling Issues:**
```bash
# Verify cloud credentials
aws sts get-caller-identity     # For AWS
gcloud auth list               # For GCP

# Test basic scaling
vrun echo "Hello Cloud"
```

**Permission Errors:**
```bash
# Ensure Docker access
sudo usermod -aG docker $USER
# Log out and back in to apply changes
```

### Reset and Cleanup

To completely reset a sandbox:

```bash
velda mini down ~/velda-sandbox
rm -rf ~/velda-sandbox
```
