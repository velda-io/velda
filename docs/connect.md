
# Connect to your Velda cluster

With the API server and at least one agent running, your cluster is ready to use.

1. Install the client commandline tool (CLI) from the [releases page](https://github.com/velda-io/velda/releases).

2. Initialize the client to connect to the cluster.
```bash
velda init --broker=${APISERVER}:50051
```

3. Create an instance from an image
```bash
velda instance create my-instance --from-image [image-name]
```

4. Your instance is ready to use. Connect to your instance and open a shell:
```bash
velda config set --instance my-instance # This is only needed one time.
velda run

# Alternatively, explicitly specify instance at every command
velda run --instance my-instance
```

# Connect with SSH or IDEs
To use other SSH client, ensure you can use `velda run` to connect in previous section, and add the following configs to `~/.ssh/config`:
```
Host [instance-name]
  HostName [instance-name]
  Port 2222
  User user
  ProxyCommand velda port-forward -W -p %p --instance %h -s ssh
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  User user
```
Replace `[instance-name]` with the actual instance name.

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