# Setup a Velda cluster

This document shows how to setup a Velda cluster step-by-step.
If you're deploying in a cloud provider(AWS, Google Cloud), you can also checkout our terraform template.
Note this document is only for open-source revision of Velda.

## Prepare the apiserver
API server is the control & data plane for Velda cluster. We recommend you to have a dedicated node for the apiserver, with a dedicated disk to store developers' data.

For this setup, we will use ZFS to store developers' data, and serves them to compute nodes through NFS.

### Setup the control plane
1. Setup ZFS and NFS for storage server
Velda open-source use ZFS to manage instances, and the data are shared using NFS.
```bash
sudo apt update && sudo apt install zfsutils-linux nfs-kernel-server nfs-common
sudo zpool create zpool /dev/[DEV_ID] # Use losetup if you want to simulate a block device.
sudo exportfs :/zpool
```

2. Download Velda API server from release page.

3. Setting up a basic config file, save it to config.yaml:
```yaml
server:
  grpc_address: ":50051"
  http_address: ":8081"

storage:
  zfs:
    pool: "zpool"

# Create the default agent pool.
agent_pools:
- name: shell
```

4. Initialize a default image. [This scripts](misc/init_agent_image.sh) will initialize an instance image from a docker image.
```bash
./misc/init_agent_image.sh veldaio/ubuntu:24.04 ubuntu_24
```

5. Start the API server
```bash
./apiserver --config config.yaml
```

### Start a runner
Runner is the node that runs the workload.
System requirement:
* Linux AMD64 or ARM64
* Direct IP access to the APIServer
* Directly reachable from the clients through IP(i.e. not behind the firewall)
* Must have CGroup v2 enabled, and cgroup v1 disabled.
* Recommended at least 1 vCPUs & 4 GB memory

#### Install the runner
1. Locate the address of apiserver
```bash
export APISERVER=127.0.0.1 # Replace with IP of the apiserver.
```
2. Install the client commandline tool (CLI)
3. Mount the volumes. Note 0 is required to indicate the shard number.
```bash
sudo mkdir -p /tmp/agentdisk/0
sudo mount -t nfs ${APISERVER}:/zpool /tmp/agentdisk/0
```
4. Setup the agent config file, and store it to `agent_config.yaml` or the system wide default `/run/velda/velda.yaml`
```yaml
broker:
  address: "${APISERVER}:50051"
```
5. Start the daemon. You must allocate a dedicated CGroup.
```bash
sudo systemd-run --unit=velda-agent -E VELDA_SYSTEM_CONFIG=$PWD/agent_config.yaml velda agent daemon --pool shell
# Stream logs
sudo journalctl -fu velda-agent.service
```

### <a name="connect"></a>Create and initialize your first instance
Once you have both apiserver and runner setup, the cluster is ready to use.
We will setup your client to connect to the cluster, and create your instance.

Please note if you're using the same machine as the runner and the client, there's a risk of config conflict. You may set `--system_config` to an non-existence file when running the following command to avoid that.

1. Locate the address of apiserver
```bash
export APISERVER=127.0.0.1 # Replace with IP of the apiserver.
```
2. Install the client commandline tool (CLI)
3. Set the apiserver address.
```bash
velda init --broker=${APISERVER}:50051
```
4. Create your instance
```bash
velda instance create first-instance --from-image ubuntu_24
```
5. Connect to your instance
```bash
velda run --instance first-instance

# Alternatively, set first-instance as your default instance
velda config set --instance first-instance # This is only needed for one-time.
velda run
```