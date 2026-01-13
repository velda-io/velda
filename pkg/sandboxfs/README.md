# Sandboxfs Caching Modes

Sandboxfs provides multiple caching modes optimized for different use cases and network environments. Each mode balances performance, consistency, and network efficiency based on your specific requirements.

## Mode Comparison

### SnapshotMode
**Aggressive caching for immutable data on network mounts**

SnapshotMode provides the highest performance by using the most aggressive caching settings. It combines both kernel-level caching and content-addressable storage (CAS) based on SHA256 hashing. This mode is designed for read-only snapshot workloads where the underlying data is guaranteed to be immutable.

**Best for:**
- Mounting immutable build artifacts or container image layers
- Accessing filesystem snapshots that won't change
- Reading from NFS or other network-mounted storage
- Archive access and browsing historical data

**Characteristics:**
- Infinite entry, attribute, and negative lookup timeouts
- All file content cached in local CAS directory
- Eager metadata prefetching
- Maximum deduplication across identical files
- Read-only access

⚠️ **Warning:** Do NOT use if data might change during mount lifetime or write access is needed.

---

### DirectFS
**Custom protocol with kernel caching for snapshot disks**

DirectFS uses a custom client/server protocol optimized for serving filesystem snapshots. The server uses Linux file handles for efficient access, while the client caches all data using content-addressable storage. All caching happens at the kernel level with persistent inodes.

Experimental.

**Best for:**
- Remote snapshot access over TCP
- Shared cache across multiple snapshots
- Snapshot traversal with minimal server round-trips
- Long-running mounts with consistent data

**Characteristics:**
- SHA256-based cache with automatic deduplication
- File handle-based protocol (no path lookups after initial mount)
- Persistent FUSE inodes with long timeouts (1 hour)
- Zero-copy design for maximum performance
- Asynchronous cache population
- Read-only access

⚠️ **Warning:** Do NOT use if data might change during mount lifetime or write access is needed.

---

### WriteOnly Mode
**Minimal caching for fast local networks**

WriteOnly mode updates cache keys without updating the actual cached content. This mode is optimized for environments with fast, low-latency networks such as cloud instances in the same region or availability zone.

**Best for:**
- Same-cloud deployments with fast interconnects
- Low-latency network environments
- Workloads where network speed rivals local disk
- Scenarios where cache storage is limited

**Characteristics:**
- Cache keys maintained for tracking
- Minimal cache storage overhead
- Relies on fast network for data access
- Lower local disk space requirements

---

### Standard Mode
**Balanced caching with CAS backend**

Standard mode provides balanced caching that updates cache keys when possible and uses content-addressable storage. This is the default mode suitable for most general-purpose use cases.

**Best for:**
- General-purpose filesystem caching
- Mixed read/write workloads
- Moderate network latencies
- Standard development and build environments

**Characteristics:**
- Content-addressable caching with SHA256
- Cache keys updated opportunistically
- Extended attributes track file hashes
- Automatic deduplication of identical content
- Supports both reads and writes
- Cache grows with unique file content

---

## Cache Architecture

All modes (except WriteOnly) use a content-addressable cache structure:

```
cache/
├── .tmp/              # Temporary files during write
├── 00/
│   └── 00a1b2c3...    # Files with SHA256 starting with "00"
├── 01/
│   └── 01d4e5f6...
└── ...
```

Files are identified by their SHA256 hash, enabling automatic deduplication across the entire cache regardless of original file paths.

## Choosing the Right Mode

| Requirement | Recommended Mode |
|-------------|-----------------|
| Immutable snapshots on NFS/network storage | **SnapshotMode** |
| Remote snapshot server over TCP | **DirectFS** |
| Same-cloud, low-latency network | **WriteOnly** |
| General-purpose with moderate network | **Standard** |
| Maximum performance for read-only data | **SnapshotMode** |
| Minimal local storage usage | **WriteOnly** |
| Write support needed | **Standard** |

## Performance Considerations

- **Cache Hits**: Read operations on cached files bypass the original filesystem
- **Cache Misses**: First access incurs hashing overhead for Standard/DirectFS modes
- **Deduplication**: Automatic across all modes using CAS (except WriteOnly)
- **Storage**: Cache grows with unique content; consider cleanup policies for long-running systems
- **Network**: SnapshotMode and DirectFS minimize network round-trips through aggressive caching
