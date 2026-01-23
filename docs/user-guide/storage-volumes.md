# Storage and Volumes

Velda provides flexible storage options for your instances and workloads. This guide covers persistent storage, shared datasets, and volume management.

## Storage Architecture

Each Velda instance has:

- **Root Filesystem**: Instance OS and system files, including home directory.
- **Additional Volumes**: Optional mounts for datasets, shared storage, etc.

## Mounting Additional Volumes

You can mount additional storage to your instance for datasets, shared storage, or temporary scratch space.

### Using /etc/fstab
Velda supports automounting of `/etc/fstab` entries for session convenience and faster startup. The agent will by default process and mount fstab entries during session startup so mounts are available without manual intervention.

Automount example (mounted during session startup):

```bash
# Connect to instance as root
velda run -u root --instance my-instance

# Edit fstab
vi /etc/fstab

# Add mount entry (mounted during session startup)
# [source] [mount-point] [type] [options] [dump] [pass]
storage-server:/datasets  /data  nfs  defaults  0  0

# Create mount point
mkdir -p /data
```

## Lazy mounts

Most filesystem has some overhead during mount or have cap on clients, while not all sessions require these. mount.
You can mark them lazy by appending the `x-lazy` option to the mount entry. When `x-lazy` is present the agent will mount when the mountpoint is accessed (using autofs) instead of mounting it immediately.

Example:

```bash
# Add fstab entry with x-lazy to enable on-demand mounting
storage-server:/datasets  /data  nfs  x-lazy,defaults  0  0
```

After saving `/etc/fstab`, accessing the directory (for example `ls /data`) triggers the lazy mount on first access for new sessions.

## Host mounts

Velda supports projecting directories from the underlying host machine directly into your instance. This is useful for:
- Accessing local datasets without network overhead.
- Persisting intermediate artifacts to the host, e.g. caches or logs.
- Sharing configuration files.

Use the filesystem type `host` when declaring a host-backed mount. The agent will perform a bind mount of the host path into the instance workspace. For security, host mount sources should be under `/tmp/shared` and needs to be pre-configured by the cluster (or use the EmptyDir pattern below).

Example host mount (`/etc/fstab`):

```
/tmp/shared/mydata /data host defaults 0 0 
```

- The agent supports read-write and read-only host mounts; read-only mounts are remounted as read-only by the agent.

## Ephemeral Volumes (EmptyDir)

For temporary storage needs, Velda provides **EmptyDir** volumes. These are:
- **Ephemeral**: Data is lost when the instance is stopped.
- **Fast**: Backed by the host's local storage (or tmpfs when configured).
- **Secure**: Created with isolated permissions (0777) for the instance.

An EmptyDir is specified using a source pattern like `<empty>` together with `fstype: host`. When the agent sees a host mount request whose source matches the `<empty>` pattern, it creates a unique temporary directory on the host and mount to the host. It will be deleted once the session is terminated.

Example empty dir mount (`/etc/fstab`):
```
<empty> /data host defaults 0 0 
```
