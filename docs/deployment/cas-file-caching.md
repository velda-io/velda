# CAS File Caching

Content Addressable Storage (CAS) is a local disk cache that agents use to avoid re-fetching files they have already seen. When a session needs a file from the file server, the agent checks the CAS cache first. On a cache hit the file is served from local disk, removing the round-trip to the file server entirely. The cache is keyed by the file content, so the same cache can be reused regardless on how the package is installed. This is especially valuable when:

- Large dependencies and packages used in machine learning, e.g. PyTorch, Cuda, CUDNN, etc.
- Agents are on remote or slow networks (multi-cloud, WireGuard / Tailscale tunnels).
- Many sessions on the same agent re-use the same snapshot data set.
- You run repeated experiments over the same data files.

There are two protocols used by Velda to access files from the data server:

- NFS: for regular read-write access, good for writing or same-cloud same region access. Once fetched, it will be saved to local cache. Slower on metadata exchanges.
- Custom protocol: High-performance & CAS aware, currently only support read-only access for immutable data (snapshots).

## How it works

1. The Velda file server (running on the controller host) tags each file with a `sha256` hash stored in an extended file attribute.
2. When the agent's sandboxfs layer requests a file, the agent looks up the hash in the CAS cache directory.
3. On a **miss** the agent fetches the file from the file server and stores it in the cache keyed by hash.
4. On a **hit** the agent reads directly from the local cache and the file server is not contacted.

Because the cache is keyed by content hash, files that appear in multiple snapshots or writable-dir copies are only stored once on disk.

## Set up CAS

The minimum config required is to add `cas_config` to the agent config.

### Setting up the file server

Some Terraform-based deployments add the fileserver systemd unit and enable it by default.
```bash
sudo systemctl enable velda-fileserver
sudo systemctl start velda-fileserver
```

You can also start manually:
```bash
sudo velda fileserver \
  --addr  :7655       \
  --root  /zpool      \
```

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `localhost:7655` | TCP address to listen on. Use `:7655` to accept agent connections. |
| `--root` | `.` | Root path to serve. Set to `/zpool` so the server can reach ZFS pool dataset paths. |

!!! tip
    Open TCP port **7655** (or whichever port you choose) in your firewall/security-group so agents can reach the file server.

### Agent configuration

CAS is configured in the `sandbox_config.disk_source.cas_config` section of the agent configuration file (often part of agent launch config).

#### Minimal configuration
cas_config will enable the CAS key update. If no cache is desired on the specific cache pool, still provide an empty config (`cas_config: {}`) to make it updating CAS key when writing.

```yaml
sandbox_config:
  disk_source:
    nfs_mount_source: {}
    cas_config:
      cas_cache_dir: /var/lib/velda/velda_cas_cache
      use_direct_protocol: true
```

#### All `cas_config` fields

```yaml
cas_config:
  # Directory on the agent host where cached files are stored.
  # Must be writable by the agent process.
  cas_cache_dir: /var/lib/velda/velda_cas_cache

  # Enable the custom fetch protocol with file server instead of the NFS based protocol.
  # The direct protocol has lower overhead and is recommended when using the
  # built-in Velda file server.
  use_direct_protocol: true

  # Additional flags passed to the sandboxfs CAS layer.
  # Consult sandboxfs documentation for available flags.
  flags: []

  # Additional cache sources consulted before falling back to the file server.
  # Each entry is a URL (HTTP/HTTPS) or an NFS path.
  # Files are looked up by (size, sha256) key.
  # Example: ["https://cas.velda.io"]
  cache_sources: []
```

### Using additional cache sources

If you maintain a separate HTTP cache tier (for example an internal object-store proxy or a region-local NFS share), add it to `cache_sources`. The agent tries each source in order before falling back to the primary file server:

```yaml
cas_config:
  cas_cache_dir: /tmp/velda_cas_cache
  use_direct_protocol: true
  cache_sources:
    - "http://regional-cache.internal:7655"
```

This is useful in multi-region setups where a regional cache replica is much closer to the agents than the primary controller.

### Laucnh jobs with snapshots

The use of custom protocol requires to read from an immutable data source, e.g. snapshot. You can launch jobs with  [snapshots & writable overlays](../user-guide/snapshots-and-overlays.md):

- Snapshots provide a read-only base for workloads, avoid re-lookup on cache invalidation, improve prefetching.
- CAS ensures that once a snapshot's blocks have been fetched on an agent node, subsequent jobs on the same node pay no network cost for those blocks.


```bash
# Setting a --writable-dir automatically create a per-job ephemeral snapshot.
vbatch --writable-dir /results python train.py
```

## Optimizing performance
For optimal configurations which can enable multi-cloud scaling with near-local performance:

1. Launch job with snapshot or writable-dir set.
2. Start the file server.
3. All agents enabled cas in the agent config
4. Use extra cache (e.g. R2) for large files
5. If possible, enable "parked instance" for the cloud, which will keep the disk when shutting down compute.
6. If using tailscale to connect, ensure it has direct connection and not fallback to DERP.
