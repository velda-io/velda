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
sudo exportfs -o rw,no_root_squash :/zpool
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

4. Initialize a default image. [This script](/core/oss/misc/init_agent_image.sh) will initialize an instance image from a Docker image.
```bash
./misc/init_agent_image.sh veldaio/ubuntu:24.04 ubuntu_24
```
New images can be created from any existing instance.

5. Start the API server
```bash
./apiserver --config config.yaml
```

The control plane is now ready. It's strongly recommended to ensure only authorized people (e.g., admin) have access to the control plane. Unauthorized access could grant complete access to all files in the system.

### Start a runner
Runner is the node that runs the workload.
System requirements:
* Linux AMD64 or ARM64
* Direct IP access to the APIServer
* Directly reachable from the clients through IP (i.e., not behind a firewall)
* Must have CGroup v2 enabled, and cgroup v1 disabled
* Recommended at least 1 vCPU & 4 GB memory

#### Install the runner
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

With the api server and at least one agent running, your cluster is ready to use. See [connect to your cluster](connect.md).