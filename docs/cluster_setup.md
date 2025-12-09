# Set up a Velda cluster

This document shows how to set up an open-source Velda cluster directly on Linux machines.

If you're deploying in a cloud provider, you can also check out our Terraform template.
* [Google cloud](terraform_gcp.md)


## Prepare the apiserver
API server is the control & data plane for a Velda cluster. We recommend having a dedicated node for the apiserver, with a dedicated disk to store developers' data.

We will use ZFS to store developers' data, and serve them to compute nodes through NFS.

### Set up the control plane
1. Set up ZFS and NFS for storage server:
   Velda open-source uses ZFS to manage instances, and the data is shared using NFS.
```bash
sudo apt update && sudo apt install zfsutils-linux nfs-kernel-server nfs-common
sudo zpool create zpool /dev/[DEV_ID] # Use losetup if you want to simulate a block device.
```

2. Download the Velda API server from the [release page](https://github.com/velda-io/velda/releases).

3. Set up a basic config file, save it to config.yaml:
```yaml
server:
  grpc_address: ":50051"
  http_address: ":8081"

storage:
  zfs:
    pool: "zpool"

# Create the default agent pool
agent_pools:
- name: shell
```

4. Initialize a default image. You can create an instance from a Docker image directly using the `velda` CLI.

From a Docker image (requires Docker available where you run this command):
```bash
velda instance create ubuntu_24 --docker-image ubuntu:24.04
```

New images can be created from any existing instance.

5. Start the API server
```bash
./apiserver --config config.yaml
```

The control plane is now ready. It's strongly recommended to ensure only authorized people (e.g., admin) have access to the control plane. Unauthorized access could grant complete access to all files in the system.

## Start a runner
Runner is the node that runs the workload.
System requirements:
* Linux AMD64 or ARM64
* Direct IP access to the APIServer
* Directly reachable from the clients through IP (i.e., not behind a firewall)
* Must have CGroup v2 enabled, and cgroup v1 disabled
* Recommended at least 1 vCPU & 4 GB memory

### Install the runner
1. Locate the address of the apiserver:
```bash
export APISERVER=127.0.0.1 # Replace with IP of the apiserver.
```

2. Install the client commandline tool (CLI) from the [releases page](https://github.com/velda-io/velda/releases).

3. Mount the volumes. Note: 0 is required to indicate the shard number.
```bash
sudo mkdir -p /tmp/agentdisk/0
sudo mount -t nfs ${APISERVER}:/zpool /tmp/agentdisk/0
```

4. Set up the agent config file, and store it to `agent_config.yaml` or the system-wide default `/run/velda/velda.yaml`:
```yaml
broker:
  address: "${APISERVER}:50051"
```

5. Start the daemon. You must allocate a dedicated CGroup, so we used systemd-run for that.
```bash
sudo systemd-run --unit=velda-agent -E VELDA_SYSTEM_CONFIG=$PWD/agent_config.yaml velda agent daemon --pool shell
# Stream logs
sudo journalctl -fu velda-agent.service
```

Repeat the steps above for all the worker nodes.


## Initialize the first instance & image in the cluster from a docker container

With the api server and at least one agent running, your cluster is ready to use.
Let's start with creating a velda instance from a docker container image, and save it as an image to create other instances.

1. Locate the address of the apiserver:
```bash
export APISERVER=127.0.0.1 # Replace with IP of the apiserver.
```

2. Install the client commandline tool (CLI) from the [releases page](https://github.com/velda-io/velda/releases).

3. Initialize the client to connect to the cluster.
```bash
velda init --broker=${APISERVER}:50051
```

4. Create an empty instance, and initialize it with a container image:
```bash
# Create an instance and populate it from a Docker image
velda instance create first-instance --docker-image ubuntu:24.04

# Or, if you have an exported tar filesystem, import it instead
velda instance create first-instance --tar-file /path/to/ubuntu-24.04.tar
```
Most images do not have development dependencies installed. To add some basic dependencies:
```bash
velda run -u root -i first-instance bash -c 'apt update && apt install less curl git man sudo bash-completion ca-certificates psmisc -y --no-install-recommends;
which unminimize || apt install unminimize && yes | unminimize'
```

5. Your instance is ready to use. Connect to your instance:
```bash
velda config set --instance first-instance # This is only needed one time.
velda run

# Alternatively, explicity specify instance at every command
velda run --instance first-instance
```

6. Install necessary dependencies. Create an image for everyone to use.
```bash
velda image create --from-instance first-instance base-image
```

## Create instances from an image / Connect to instances
See [connect to your cluster](connect.md).