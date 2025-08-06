# Set up a Velda cluster

This document shows how to set up a Velda cluster step-by-step.
If you're deploying in a cloud provider (AWS, Google Cloud), you can also check out our Terraform template.
Note: This document is only for the open-source version of Velda.

## Prepare the apiserver
API server is the control & data plane for a Velda cluster. We recommend having a dedicated node for the apiserver, with a dedicated disk to store developers' data.

For this setup, we will use ZFS to store developers' data, and serve them to compute nodes through NFS.

### Set up the control plane
1. Set up ZFS and NFS for storage server:
   Velda open-source uses ZFS to manage instances, and the data is shared using NFS.
```bash
sudo apt update && sudo apt install zfsutils-linux nfs-kernel-server nfs-common
sudo zpool create zpool /dev/[DEV_ID] # Use losetup if you want to simulate a block device.
sudo exportfs :/zpool
```

2. Download the Velda API server from the release page.

3. Set up a basic config file, save it to config.yaml:
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

4. Initialize a default image. [This script](misc/init_agent_image.sh) will initialize an instance image from a Docker image.
```bash
./misc/init_agent_image.sh veldaio/ubuntu:24.04 ubuntu_24
```
New images can be created from any existing instance.

5. Start the API server
```bash
./apiserver --config config.yaml
```

The control plane is now ready. It's strongly recommended to ensure only authorized people(e.g. admin) have access to the control plane. Granting the access could grant all file access.

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

2. Install the client commandline tool (CLI).

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

5. Start the daemon. You must allocate a dedicated CGroup.
```bash
sudo systemd-run --unit=velda-agent -E VELDA_SYSTEM_CONFIG=$PWD/agent_config.yaml velda agent daemon --pool shell
# Stream logs
sudo journalctl -fu velda-agent.service
```

### <a name="connect"></a>Create and initialize your first instance
Once you have both apiserver and runner set up, the cluster is ready to use.
We will set up your client to connect to the cluster and create your instance.

Please note: If you're using the same machine as the runner and the client, there's a risk of config conflict.

1. Locate the address of the apiserver:
```bash
export APISERVER=127.0.0.1 # Replace with IP of the apiserver.
```

2. Install the client commandline tool (CLI).

3. Set the apiserver address:
```bash
velda init --broker=${APISERVER}:50051
```

4. Create your instance:
```bash
velda instance create first-instance --from-image ubuntu_24
```

5. Connect to your instance:
```bash
velda run --instance first-instance

# Alternatively, set first-instance as your default instance
velda config set --instance first-instance # This is only needed one time.
velda run
```

# What's next

### Control the access to an instance
<details>
For the Open-source edition, access control is implemented through SSH keys.

By default, every instance is accessible to everyone in the same network.

Access control will be enforced if any authorized key is added.

Authorized keys are stored at `/.velda/authorized_keys`.

```bash
# Generate an SSH key
ssh-keygen -f ~/.ssh/velda -t ed25519
# Add the key to authorized_keys
cat ~/.ssh/velda.pub | velda run -u root bash -c 'cat >> /.velda/authorized_keys'
velda config set --identity-file ~/.ssh/velda
```

To access the instance after setting up the identity file:
```bash
velda run
```
Alternatively, use the full command:
```bash
velda run --instance [instance-name] --identity-file [path-to-private-key]
```

#### Reset access
If you lose access, a cluster admin may recover access by removing the `authorized_keys` file:

1. Identify your numeric instance ID by running `velda instance list`
2. From the control plane, remove `/zpool/[instance-id]/.velda/authorized_keys`
</details>