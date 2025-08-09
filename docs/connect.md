
# Connect to your Velda cluster

### Create and initialize your first instance
Once you have both apiserver and runner set up, the cluster is ready to use.
We will set up your client to connect to the cluster and create your instance.

Please note: If you're using the same machine as the runner and the client, there's a risk of config conflict.

1. Locate the address of the apiserver:
```bash
export APISERVER=127.0.0.1 # Replace with IP of the apiserver.
```

2. Install the client commandline tool (CLI) from the [releases page](https://github.com/velda-io/velda/releases).

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

## Control the access to an instance

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

### Reset access
If you lose access, a cluster admin (someone with direct access to the apiserver node) may recover access by removing the `authorized_keys` file:

1. Identify your numeric instance ID by running `velda instance list`
2. From the control plane, remove `/zpool/[instance-id]/.velda/authorized_keys`