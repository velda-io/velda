# Cached Loopback FUSE Filesystem

This implementation provides a custom loopback FUSE filesystem with content-addressable caching based on SHA256 hashing and extended attributes (xattr).

## Architecture

### Components

1. **CacheManager Interface** ([cache.go](cache.go))
   - Manages content-addressable storage using SHA256 hashing
   - Provides `Lookup`, `CreateTemp`, and `Remove` operations
   - Directory-based implementation stores files in subdirectories based on hash prefix

2. **CachedLoopbackNode** ([cached_loopback.go](cached_loopback.go))
   - Custom FUSE node implementation extending go-fuse's LoopbackNode
   - Implements xattr-based cache tracking
   - Handles cache lookup and invalidation

3. **CachedFileHandle** ([cached_loopback.go](cached_loopback.go))
   - File handle with sequential read/write caching
   - Computes SHA256 on file close
   - Manages cache commit/abort operations

## Features

### 1. Content-Addressable Caching

Files are cached based on their SHA256 hash, stored in a directory structure:
```
cache/
├── .tmp/              # Temporary files during write
├── 00/
│   └── 00a1b2c3...    # File with SHA256 starting with "00"
├── 01/
│   └── 01d4e5f6...
└── ...
```

### 2. Extended Attributes (xattr)

The filesystem uses the `user.veldafs.sha256` xattr to store file hashes:
- Written when a file is closed after sequential read/write
- Checked during inode lookup to redirect reads to cached files
- Invalidated on any write operation

### 3. Sequential Read/Write Caching

**For Writes:**
- Content is written to both the original file and a temporary cache file
- On file close, SHA256 is computed and the cache file is committed
- The xattr is updated with the SHA256 hash and mtime
- Any `seek` operation aborts caching

**For Reads:**
- If xattr exists, mtime match and cache is available, reads are redirected to cached file
- Otherwise, content is hashed during sequential reads
- On close, if a full sequential read occurred, the xattr is updated

## Usage

### Basic Usage

```go
import "velda/pkg/sandboxfs"

// Create cache manager
cache, err := sandboxfs.NewDirectoryCacheManager("/path/to/cache")
if err != nil {
    log.Fatal(err)
}

// Mount with caching
server, err := sandboxfs.MountWorkDirWithCache(
    "/path/to/source",
    "/path/to/mount",
    "/path/to/cache",
)
if err != nil {
    log.Fatal(err)
}
defer server.Unmount()

// Wait for mount
server.WaitMount()
```

### Custom Cache Manager

Implement the `CacheManager` interface for custom storage backends:

```go
type CacheManager interface {
    Lookup(sha256sum string) (string, error)
    CreateTemp() (CacheWriter, error)
    Remove(sha256sum string) error
}

type CacheWriter interface {
    io.Writer
    io.WriterAt
    Commit() (sha256sum string, err error)
    Abort() error
}
```

## Performance Considerations

### Cache Hits
- Read operations on cached files bypass the original filesystem
- Enables deduplication of identical files
- Reduces I/O on the backing filesystem

### Cache Misses
- First read incurs hashing overhead
- Sequential reads update cache for future access
- Non-sequential access (seeks) disables caching

### Storage
- Cache grows with unique file content
- Deduplication is automatic (same content = same SHA256)
- Consider implementing cache eviction based on LRU or size limits

## Implementation Details

### Thread Safety
- `DirectoryCacheManager` uses `sync.RWMutex` for concurrent access
- Read operations (Lookup) use read locks
- Write operations (CreateTemp, Remove) use write locks

### Error Handling
- Cache failures don't block file operations
- Falls back to direct file access if cache unavailable
- Errors during cache write abort caching but complete file operation

### FUSE Integration
- Uses `go-fuse/v2` NodeAPI
- Implements `NodeOpener`, `NodeGetxattrer`, `NodeSetxattrer`
- File handles implement `FileReader`, `FileWriter`, `FileLseeker`, `FileReleaser`

## Limitations

1. **Sequential Access Only**: Random access (seeks) disables caching
2. **Linux-specific**: Uses Linux xattr APIs (`user.*` namespace)
3. **No Cache Eviction**: Currently no automatic cleanup of old cache entries
4. **No Compression**: Cache stores files as-is without compression

## Future Enhancements

## Testing

Run tests:
```bash
go test -v ./pkg/sandboxfs
```
