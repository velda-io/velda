#!/bin/bash
# Script to build and run directfs client tests with sudo
# Tests require CAP_SYS_ADMIN capability for FUSE mounting and file handle operations

set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Building directfs client tests..."
go test -c -o /tmp/directfs_test -race

echo "Running tests with sudo..."
sudo /tmp/directfs_test -test.v -test.run TestSnapshotClient

# Cleanup
rm -f /tmp/directfs_test

echo "Tests completed!"
