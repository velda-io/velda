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
