// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testMounts(t *testing.T, r Runner) {
	// Create a unique test instance for mount operations
	instanceName := r.CreateTestInstance(t, "ubuntu-mounts", "ubuntu")
	defer func() {
		// Clean up the instance after tests
		_ = runVelda("instance", "delete", instanceName)
	}()

	vrunCommand := func(args ...string) error {
		return runVelda(append([]string{"run", "--instance", instanceName, "-P", "single", "-s", ""}, args...)...)
	}
	vrunCommandGetOutput := func(args ...string) (string, error) {
		return runVeldaWithOutput(append([]string{"run", "--instance", instanceName, "-P", "single", "-s", ""}, args...)...)
	}

	t.Run("HostMount", func(t *testing.T) {
		// Test host mount by adding an entry to /etc/fstab
		// First, create a shared directory on the host (simulated via /tmp/shared)
		require.NoError(t, vrunCommand("sh", "-c", `
# Add a host mount entry to /etc/fstab
sudo mkdir -p /data
sudo sh -c 'echo "/tmp/shared/test-data /data host defaults 0 0" >> /etc/fstab'
`))

		// Create a new session to trigger mount from fstab
		require.NoError(t, vrunCommand("-s", "mount-test-host", "sh", "-c", `
# Verify the mount exists
# Verify the mount exists by inspecting /proc/self/mountinfo
grep -E " \/data( |$)" /proc/self/mountinfo
# Create a test file in the mounted directory
echo "host-mount-test" | sudo tee /data/testfile-host
`))

		// Verify file was created in mounted directory
		output, err := vrunCommandGetOutput("-s", "mount-test-host-verify", "cat", "/data/testfile-host")
		require.NoError(t, err, "Failed to read file from host mount")
		assert.Equal(t, "host-mount-test\n", output, "File content should match expected output")

		// Clean up fstab entry
		require.NoError(t, vrunCommand("sh", "-c", `
sudo sed -i '/\/tmp\/shared\/test-data/d' /etc/fstab
`))
	})

	t.Run("EmptyDirMount", func(t *testing.T) {
		// Test emptydir mount by adding an entry to /etc/fstab
		require.NoError(t, vrunCommand("sh", "-c", `
# Add an emptydir mount entry to /etc/fstab
sudo mkdir -p /scratch
sudo sh -c 'echo "<empty> /scratch host defaults 0 0" >> /etc/fstab'
`))

		// Create a new session to trigger mount from fstab
		require.NoError(t, vrunCommand("-s", "mount-test-empty", "sh", "-c", `
# Verify the mount exists
# Verify the mount exists via mountinfo
grep -E " \/scratch( |$)" /proc/self/mountinfo
# Verify directory is writable
echo "emptydir-test" | sudo tee /scratch/testfile-empty
cat /scratch/testfile-empty
`))

		// Verify the emptydir is isolated per session
		// In a new session, the emptydir should be empty
		output, err := vrunCommandGetOutput("-s", "mount-test-empty-verify", "sh", "-c", `
# Check if the file exists (it shouldn't in a new emptydir)
if [ -f /scratch/testfile-empty ]; then
  echo "FILE_EXISTS"
else
  echo "FILE_NOT_EXISTS"
fi
`)
		require.NoError(t, err, "Failed to check emptydir isolation")
		assert.Contains(t, output, "FILE_NOT_EXISTS", "EmptyDir should be isolated per session")

		// Verify we can write to the new emptydir
		require.NoError(t, vrunCommand("-s", "mount-test-empty-write", "sh", "-c", `
echo "emptydir-test-2" > /scratch/testfile-empty-2
cat /scratch/testfile-empty-2
`))

		// Clean up fstab entry
		require.NoError(t, vrunCommand("sh", "-c", `
sudo sed -i '/<empty>/d' /etc/fstab
`))
	})

	t.Run("TmpfsMount", func(t *testing.T) {
		// Test tmpfs mount by adding an entry to /etc/fstab
		require.NoError(t, vrunCommand("sh", "-c", `
# Add a tmpfs mount entry to /etc/fstab
sudo mkdir -p /tmpfs-test
sudo sh -c 'echo "tmpfs /tmpfs-test tmpfs defaults,size=100M 0 0" >> /etc/fstab'
`))

		// Create a new session to trigger mount from fstab
		require.NoError(t, vrunCommand("-s", "mount-test-tmpfs", "sh", "-c", `
# Verify the mount exists and is tmpfs
# Verify the mount exists and is tmpfs via mountinfo
grep -E " \/tmpfs-test( |$).* - tmpfs " /proc/self/mountinfo
# Verify directory is writable
echo "tmpfs-test" > /tmpfs-test/testfile-tmpfs
cat /tmpfs-test/testfile-tmpfs
`))

		// Verify tmpfs behavior: file should be readable in the same session
		// and should NOT persist across a new session (tmpfs scoped to vrun)
		// Note: the initial write + read already happened inside the prior vrun
		// Now check a new session to ensure the tmpfs file does not persist
		output, err := vrunCommandGetOutput("-s", "mount-test-tmpfs-verify", "sh", "-c", `
	if [ -f /tmpfs-test/testfile-tmpfs ]; then echo "FOUND"; else echo "NOT_FOUND"; fi
	`)
		require.NoError(t, err, "Failed to check tmpfs persistence")
		assert.Contains(t, output, "NOT_FOUND", "Tmpfs file should not persist across sessions")

		// Clean up fstab entry
		require.NoError(t, vrunCommand("sh", "-c", `
sudo sed -i '/tmpfs \/tmpfs-test/d' /etc/fstab
`))
	})

	t.Run("MultipleMountsSimultaneous", func(t *testing.T) {
		// Test multiple mounts of different types simultaneously
		require.NoError(t, vrunCommand("sh", "-c", `
# Add multiple mount entries to /etc/fstab
sudo mkdir -p /data-multi /scratch-multi /tmpfs-multi
sudo sh -c 'cat >> /etc/fstab << EOF
/tmp/shared/test-data-multi /data-multi host defaults 0 0
<empty> /scratch-multi host defaults 0 0
tmpfs /tmpfs-multi tmpfs defaults,size=50M 0 0
EOF'
`))

		// Create a new session to trigger mounts from fstab
		require.NoError(t, vrunCommand("-s", "mount-test-multi", "-u", "root", "sh", "-c", `
# Verify all mounts exist
# Verify all mounts exist via mountinfo
grep -E " \/data-multi( |$)" /proc/self/mountinfo
grep -E " \/scratch-multi( |$)" /proc/self/mountinfo
grep -E " \/tmpfs-multi( |$).* - tmpfs " /proc/self/mountinfo

# Write to all mounted directories
echo "multi-host" > /data-multi/testfile-multi-host
echo "multi-empty" > /scratch-multi/testfile-multi-empty
echo "multi-tmpfs" > /tmpfs-multi/testfile-multi-tmpfs

# Verify all files are readable
cat /data-multi/testfile-multi-host
cat /scratch-multi/testfile-multi-empty
cat /tmpfs-multi/testfile-multi-tmpfs
`))

		// Verify files exist in all mounts
		hostOutput, err := vrunCommandGetOutput("-s", "mount-test-multi-verify", "-u", "root", "cat", "/data-multi/testfile-multi-host")
		require.NoError(t, err, "Failed to read file from host mount")
		assert.Equal(t, "multi-host\n", hostOutput)

		// tmpfs is scoped to the vrun session; verify it does not persist to a new session
		tmpfsOutput, err := vrunCommandGetOutput("-s", "mount-test-multi-verify2", "-u", "root", "sh", "-c", `
	if [ -f /tmpfs-multi/testfile-multi-tmpfs ]; then echo "FOUND"; else echo "NOT_FOUND"; fi
	`)
		require.NoError(t, err, "Failed to check tmpfs persistence for multi mounts")
		assert.Contains(t, tmpfsOutput, "NOT_FOUND", "Tmpfs file should not persist across sessions")

		// Clean up fstab entries
		require.NoError(t, vrunCommand("sh", "-c", `
sudo sed -i '/test-data-multi/d' /etc/fstab
sudo sed -i '/scratch-multi/d' /etc/fstab
sudo sed -i '/tmpfs-multi/d' /etc/fstab
`))
	})

	t.Run("MountPermissions", func(t *testing.T) {
		// Test that emptydir mounts have correct permissions (0777)
		require.NoError(t, vrunCommand("sh", "-c", `
# Add an emptydir mount entry to /etc/fstab
sudo mkdir -p /scratch-perm
sudo sh -c 'echo "<empty> /scratch-perm host defaults 0 0" >> /etc/fstab'
`))

		// Create a new session and verify permissions
		output, err := vrunCommandGetOutput("-s", "mount-test-perm", "sh", "-c", `
# Check permissions of the emptydir mount
stat -c '%a' /scratch-perm
`)
		require.NoError(t, err, "Failed to check emptydir permissions")
		assert.Equal(t, "777\n", output, "EmptyDir should have 0777 permissions")

		// Clean up fstab entry
		require.NoError(t, vrunCommand("sh", "-c", `
sudo sed -i '/scratch-perm/d' /etc/fstab
`))
	})

	t.Run("LazyMount", func(t *testing.T) {
		// Test lazy mount with x-lazy option
		require.NoError(t, vrunCommand("sh", "-c", `
# Add a lazy mount entry to /etc/fstab
sudo mkdir -p /lazy-mount
sudo sh -c 'echo "tmpfs /lazy-mount tmpfs x-lazy,defaults 0 0" >> /etc/fstab'
`))

		// Create a new session - inspect /proc/self/mountinfo before and after access
		out, err := vrunCommandGetOutput("-s", "mount-test-lazy", "sh", "-c", `
# Capture lines for /lazy-mount before access
before_lines=$(grep -E " \/lazy-mount( |$)" /proc/self/mountinfo || true)
before_count=$(echo "$before_lines" | sed '/^$/d' | wc -l)
# Ensure there's exactly one entry and it is NOT tmpfs
if [ "$before_count" -eq 1 ] && ! echo "$before_lines" | grep -q " - tmpfs "; then
  echo "OK_BEFORE"
else
  echo "BAD_BEFORE"
  echo "---BEFORE_LINES---"
  cat /proc/self/mountinfo
fi

# Trigger access which should cause autofs to mount the lazy mount
touch /lazy-mount/trigger-file
sleep 1

# Capture lines for /lazy-mount after access
after_lines=$(grep -E " \/lazy-mount( |$)" /proc/self/mountinfo || true)
after_count=$(echo "$after_lines" | sed '/^$/d' | wc -l)
# Expect two entries and one of them to be tmpfs
if [ "$after_count" -eq 2 ] && echo "$after_lines" | grep -q " - tmpfs " && echo "$after_lines" | grep -qv " - tmpfs "; then
  echo "OK_AFTER"
else
  echo "BAD_AFTER"
  echo "---AFTER_LINES---"
  cat /proc/self/mountinfo
fi
`)
		require.NoError(t, err, "Failed to inspect /proc/self/mountinfo for lazy mount")
		// Before access: exactly one non-tmpfs entry. After access: two entries including one tmpfs.
		require.Contains(t, out, "OK_BEFORE", "Expected one non-tmpfs entry before access in mountinfo")
		require.Contains(t, out, "OK_AFTER", "Expected two entries after access with one tmpfs in mountinfo")

		// Clean up fstab entry
		require.NoError(t, vrunCommand("sh", "-c", `
sudo sed -i '/lazy-mount/d' /etc/fstab
`))
	})
}
