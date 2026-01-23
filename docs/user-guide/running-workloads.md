# Running Workloads in Velda

This guide explains how to run workloads in Velda using the `vrun` command, including running on different resource pools, reattaching to sessions, and connecting to open ports.

## The `vrun` Command

`vrun` is Velda's primary command for executing workloads on cloud resources. Prefix any command with `vrun` to run it on your Velda Infrastructure after connected to your Velda instance.

`vrun` will run the command on a different node with different compute setup, while keep the environment (including files, packages, environment variables) same as your instance.

### Basic Syntax

```bash
vrun [OPTIONS] COMMAND [ARGS...]
```

## Resource Pools

Resource pools define pre-configured compute profiles. Use the `-P` flag to specify which pool to use.

The available pool configurations vary by cluster.

### Using Resource Pools

```bash
# List available pools
velda pool list

# All pool names below are for reference only.

# Run CPU-intensive compilation
vrun -P cpu-large make -j 16

# Run GPU training
vrun -P gpu-t4 python train.py

# High-memory data processing
vrun -P mem-xlarge python process_large_dataset.py

# Multi-GPU distributed training
vrun -P gpu-4xa100 torchrun --nproc_per_node=4 train.py
```

### Default Pool

If you don't specify a pool, `vrun` uses the default `shell` pool, which typically provides a single CPU instance.

```bash
# Runs on default pool
vrun echo "Hello World"
```

## Working with Sessions

Sessions allow you to run multiple commands in the same physical VM and reattach to running workloads.

### Creating Named Sessions

Use the `-s` flag to assign a custom session name:

```bash
# Start a workload in a named session
vrun -s training -P gpu-a100 python train.py
```

### Reattaching to Sessions

Run additional commands in the same session from any terminal:

```bash
# Check GPU usage in the training session
vrun -s training nvidia-smi

# Open an interactive shell
vrun -s training bash
```

### Session Characteristics

- Commands in the same session run on the same VM
- Multiple commands share allocated resources
- Sessions terminates when disconnected

## Port Forwarding and Network Access

Velda provides several methods to connect to open ports from running services.

### 1. Use Session Name as Hostname

The simplest way is to use the session name directly as the hostname. This works when connecting from another session on the same instance.

The service must allow connection from external address.

```bash
# Start a web server in a session
vrun -s web python -m http.server 8000 &

# Access from another session
curl web:8000
```

### 2. Port-Forward While Running Workload

Forward a port while launching your workload, making it accessible via localhost within the instance:

```bash
# Forward port and run service
vrun -p 8000:8000 python -m http.server 8000 &

# Access via localhost
curl localhost:8000
```

### 3. Port-Forward for Browser or Remote Access

Enable access from your local machine to services running in Velda:

**Step 1**: Start your workload in a session

```bash
vrun -s web python -m http.server 8000
```

**Step 2**: From your local machine (requires Velda CLI)

```bash
velda port-forward -s web --port 8000 -l :8000
```

After running this command, access the service at `http://localhost:8000` in your local browser.

### 4. Use Proxy (Velda Cloud / Enterprise only)

Most Velda deployments include a proxy server for easy HTTP/HTTPS access.

If your workload runs as:

```bash
vrun -s web python -m http.server 8000
```

And your Velda service is hosted at `velda.example.com`, access it at:

```
http://8000-web-[instance-name].i.velda.example.com
```

**Note**: For security, this is currently enterprise/hosted only.

### 5. Use VS Code (or Your IDE) Port Forwarding

If you're connected via VS Code, you can use its built-in port-forwarding feature:

1. Open the Ports panel in VS Code
2. Click "Forward a Port"
3. Enter the port number (e.g., 8000)
4. Access via `localhost:8000` on your local machine
5. Use port-forwarding with `vrun` to forward ports from other sessions.

## Best Practices

### 1. Use Named Sessions for Important Workloads

```bash
# Good: Named session for easy reattachment
vrun -s my-training python train.py

# Less ideal: Anonymous session
vrun python train.py
```

### 2. Match Resource Pool to Workload

```bash
# Good: GPU pool for GPU workload
vrun -P gpu-a100 python train.py

# Wasteful: GPU pool for CPU-only workload
vrun -P gpu-a100 python preprocess.py  # Should use cpu-large
```

### 3. Use Port Forwarding for Services

```bash
# Good: Explicit port forwarding
vrun -s jupyter -p 8888:8888 jupyter notebook

# Less secure: Exposing on all interfaces without forwarding
vrun jupyter notebook --ip=0.0.0.0
```
