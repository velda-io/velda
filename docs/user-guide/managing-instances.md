# Managing Instances

Instances are your personal development environments in Velda. Each instance has persistent storage, can run multiple sessions, and provides a consistent workspace for your code and data.

## What is an Instance?

An instance in Velda is:

- A **persistent Linux environment** with your files, tools, and configurations
- **Accessible via SSH, VS Code, or web IDE**
- Capable of running **multiple concurrent sessions** with `vrun`
- **Isolated from other users** for security and resource management

## Creating Instances

To run `velda` command, you need to run from an existing velda instance in the cluster, or ssh to admin user on controller, or download `velda` CLI from [release](https://github.com/velda-io/velda/releases).

### From an Image

Create an instance from a pre-configured image in your cluster:

```bash
# Create from base image
velda instance create my-instance --from-image base-image

# Create from specific image version
velda instance create my-instance --from-image ubuntu-ml:v2
```

### From a Docker Image

Bootstrap a new instance from any Docker image:

```bash
# From public Docker Hub image
velda instance create my-instance --docker-image ubuntu:24.04

# From private registry
velda instance create my-instance --docker-image registry.company.com/custom:latest
```

## Listing Instances

View all your instances:

```bash
# List all instances
velda instance list
```

## Connecting to Instances

Multiple ways to access your instance:

### Via SSH/CLI
```bash
# Set default instance
velda config set --instance my-instance

# Connect
velda run

# Or specify instance each time
velda run --instance my-instance
```

## Managing Instance State

### Starting and Stopping

```bash
# Stop a running instance
velda instance stop my-instance

# Start a stopped instance
velda instance start my-instance

# Restart an instance
velda instance restart my-instance
```

## Creating Images from Instances

Save an instance as an image for reuse:

```bash
# Create image from instance
velda image create --from-instance my-instance my-custom-image
```

**Benefits:**
- **Reproducible environments**: Share exact setup with team
- **Quick provisioning**: New instances start from saved state
- **Version control**: Create images at different stages

## Deleting Instances

Remove instances you no longer need:

```bash
# Delete an instance
velda instance delete my-instance
```

⚠️ **Warning**: Deleting an instance permanently removes all data. Create an image first if you want to preserve the setup.

## Instance Customization

### Installing Software

Velda has almost no restriction on package management options.

```bash
# Install packages
sudo apt update && sudo apt install -y vim tmux htop

# Install Python packages
pip install torch transformers datasets

# Install with conda
conda install -c conda-forge scikit-learn
```

### Persist Configuration

All changes to the file system are automatically persisted:
- Installed packages
- Code and data files
- SSH keys and configurations

## Access Control

### Managing SSH Keys

For open-source Velda, access is controlled via SSH keys:

```bash
# Generate SSH key
ssh-keygen -f ~/.ssh/velda -t ed25519

# Add key to instance
cat ~/.ssh/velda.pub | velda run -u root bash -c 'cat >> /.velda/authorized_keys'

# Configure CLI to use key
velda config set --identity-file ~/.ssh/velda
```

### Adjusting Resources

Resource allocation is managed by the `vrun` pool selection. The instance itself is lightweight and only storage is used; workloads run on pool resources.

## Troubleshooting

### Lost Access (SSH Keys)

**Issue**: Locked out due to misconfigured SSH keys

**Solution** (requires admin access):
```bash
# Admin removes authorized_keys file
rm /zpool/[instance-id]/.velda/authorized_keys
```
