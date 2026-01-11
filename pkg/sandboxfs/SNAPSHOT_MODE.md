# Snapshot Mode

## Overview

Snapshot mode is an optional configuration for VeldaFS that maximizes IO performance by using the most aggressive caching settings. This mode is designed for read-only snapshot workloads where the underlying data is known to be immutable.

## Features

When snapshot mode is enabled, the following aggressive caching strategies are applied:

1. **Infinite Entry Timeout**: Directory entry cache is never invalidated (365 days)
2. **Infinite Attribute Timeout**: File/directory attributes are cached indefinitely
3. **Infinite Negative Timeout**: Negative lookups (file not found) are cached for a very long time
4. **Aggressive Content Caching**: All file content is cached in the local cache directory
5. **Stat/Xattr Prefetching**: File metadata is eagerly prefetched for better performance
6. **Symlink Caching**: Symlinks are fully cached and never revalidated

## Use Cases

Snapshot mode is ideal for:

- **Build Artifacts**: Mounting immutable build output directories
- **Container Image Layers**: Accessing read-only container filesystems
- **Data Snapshots**: Reading from filesystem snapshots that won't change
- **Archive Access**: Browsing historical data that is guaranteed to be static

⚠️ **Warning**: Do NOT use snapshot mode if:
- The underlying data might change during the mount lifetime
- You need write access to the filesystem
- Multiple processes might be modifying the source directory

## Usage

### Command Line

Enable snapshot mode using the `--snapshot` flag:

```bash
velda-agent sandboxfs /path/to/source /path/to/mountpoint --snapshot
```

### Programmatic API

Use the `WithSnapshotMode()` option when mounting:

```go
import (
    "velda.io/velda-core/oss/pkg/sandboxfs"
)

server, err := sandboxfs.MountWorkDir(
    baseDir,
    workspaceDir,
    cacheDir,
    sandboxfs.WithSnapshotMode(),
)
```

### Combining with Other Options

You can combine snapshot mode with other mount options:

```go
server, err := sandboxfs.MountWorkDir(
    baseDir,
    workspaceDir,
    cacheDir,
    sandboxfs.WithSnapshotMode(),
    func(opts *fs.Options) {
        opts.FsName = "my-snapshot-fs"
        opts.Debug = true
    },
)
```

## Performance Characteristics

Snapshot mode provides significant performance improvements for read-heavy workloads:

- **First Access**: Files are read from source and cached locally
- **Subsequent Access**: Cached files are served directly without source filesystem access
- **Metadata Operations**: Stats, directory listings, and symlink resolutions are served from cache
- **No Revalidation**: The kernel never asks FUSE to revalidate cached data

### Benchmark Results

In typical read-only workload scenarios, snapshot mode can provide:
- **10-100x faster** repeated file access
- **Near-zero** metadata overhead after first access
- **Minimal** source filesystem load

## Implementation Details

The snapshot mode implementation:

1. Sets `EntryTimeout` and `AttrTimeout` to 365 days (effectively infinite)
2. Marks the `MountContext.SnapshotMode` flag as true
3. Maintains all existing caching logic but never invalidates cached data
4. Continues to use background workers for eager fetching and prefetching

## Caveats

- The mount will not reflect any changes to the source directory after mounting
- Cache space is managed independently - ensure sufficient cache directory space
- Unmounting and remounting is required to see updates to source data
- Extended attributes and other metadata follow the same caching behavior

## See Also

- [cached_loopback.go](./cached_loopback.go) - Core implementation
- [fs.go](./fs.go) - Mount options and configuration
