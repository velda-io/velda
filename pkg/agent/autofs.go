// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	autofsDevice                   = "/dev/autofs"
	AUTOFS_DEV_IOCTL_VERSION_MAJOR = 1
	AUTOFS_DEV_IOCTL_VERSION_MINOR = 1
	AUTOFS_PROTO_VERSION           = 5
	AUTOFS_PROTO_SUBVERSION        = 6

	/* Indirect mount missing and expire requests. */
	AUTOFS_PTYPE_MISSING_INDIRECT = 3
	AUTOFS_PTYPE_EXPIRE_INDIRECT  = 4

	/* Direct mount missing and expire requests */
	AUTOFS_PTYPE_MISSING_DIRECT = 5
	AUTOFS_PTYPE_EXPIRE_DIRECT  = 6
)

type autofsIoctlCommand uintptr

const (
	/* Get various version info */
	AUTOFS_DEV_IOCTL_VERSION_CMD autofsIoctlCommand = iota + 0x9371
	AUTOFS_DEV_IOCTL_PROTOVER_CMD
	AUTOFS_DEV_IOCTL_PROTOSUBVER_CMD

	/* Open mount ioctl fd */
	AUTOFS_DEV_IOCTL_OPENMOUNT_CMD

	/* Close mount ioctl fd */
	AUTOFS_DEV_IOCTL_CLOSEMOUNT_CMD

	/* Mount/expire status returns */
	AUTOFS_DEV_IOCTL_READY_CMD
	AUTOFS_DEV_IOCTL_FAIL_CMD

	/* Activate/deactivate autofs mount */
	AUTOFS_DEV_IOCTL_SETPIPEFD_CMD
	AUTOFS_DEV_IOCTL_CATATONIC_CMD

	/* Expiry timeout */
	AUTOFS_DEV_IOCTL_TIMEOUT_CMD

	/* Get mount last requesting uid and gid */
	AUTOFS_DEV_IOCTL_REQUESTER_CMD

	/* Check for eligible expire candidates */
	AUTOFS_DEV_IOCTL_EXPIRE_CMD

	/* Request busy status */
	AUTOFS_DEV_IOCTL_ASKUMOUNT_CMD

	/* Check if path is a mountpoint */
	AUTOFS_DEV_IOCTL_ISMOUNTPOINT_CMD
)

type AutoFS struct {
	devFd   int
	timeout int
}

type autofsDevIoctl struct {
	VerMajor uint32
	VerMinor uint32
	Size     uint32
	IoctlFd  int32
	Args1    uint32
	Args2    uint32
}

var ioctlSize = unsafe.Sizeof(autofsDevIoctl{})

type autofsDevIoctlWithPath struct {
	autofsDevIoctl
	Path [256]byte
}

func (v *autofsDevIoctl) setTimeout(timeout uint64) {
	v.Args1 = uint32(timeout)
	v.Args2 = uint32(timeout >> 32)
}

type autofsPacket struct {
	ProtoVersion   int32
	Type           int32
	WaitQueueToken uint32
	Dev            int32
	Ino            uint64
	Uid            uint32
	Gid            uint32
	Pid            uint32
	TGid           uint32
	NameLen        uint32
	Name           [256]byte
}

func NewAutoFS() (*AutoFS, error) {
	fd, err := syscall.Open(autofsDevice, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", autofsDevice, err)
	}

	return &AutoFS{devFd: fd}, nil
}

func (a *AutoFS) Mount(mountPoint string, mountFunc func(mountpoint string) error) error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}

	err = syscall.Mount("none", mountPoint, "autofs", syscall.MS_MGC_VAL,
		fmt.Sprintf("fd=%d,minproto=5,maxproto=5,direct", w.Fd()))

	if err != nil {
		return fmt.Errorf("failed to mount autofs: %w", err)
	}
	ioctlfd, err := a.openIoctlFd(mountPoint)
	if err != nil {
		return err
	}

	major, _, err := a.ioctl(ioctlfd, AUTOFS_DEV_IOCTL_PROTOVER_CMD, AUTOFS_PROTO_VERSION, 0)
	if err != nil {
		return fmt.Errorf("failed to get protocol version: %w", err)
	}
	minor, _, err := a.ioctl(ioctlfd, AUTOFS_DEV_IOCTL_PROTOSUBVER_CMD, AUTOFS_PROTO_SUBVERSION, 0)
	if err != nil {
		return fmt.Errorf("failed to get protocol subversion: %w", err)
	}
	log.Printf("AutoFS protocol version: %d.%d\n", major, minor)

	go func() {
		for {
			packet := autofsPacket{}
			err := binary.Read(r, binary.LittleEndian, &packet)
			if err != nil {
				log.Printf("failed to read autofs packet: %v\n", err)
				return
			}
			switch packet.Type {
			case AUTOFS_PTYPE_MISSING_INDIRECT, AUTOFS_PTYPE_MISSING_DIRECT:
				// Perform mount
				log.Printf("Mounting %v %v, requested by PID %d\n", packet.Name[:packet.NameLen], mountPoint, packet.Pid)
				err = mountFunc(mountPoint)
				notifyCmd := AUTOFS_DEV_IOCTL_READY_CMD
				if err != nil {
					log.Printf("failed to mount %s: %v\n", mountPoint, err)
					notifyCmd = AUTOFS_DEV_IOCTL_FAIL_CMD
				}
				_, _, err = a.ioctl(ioctlfd, notifyCmd, uint32(packet.WaitQueueToken), 0)
				if err != nil {
					log.Printf("failed to signal ready: %v\n", err)
					return
				}
				log.Printf("Mount done\n")
			case AUTOFS_PTYPE_EXPIRE_INDIRECT, AUTOFS_PTYPE_EXPIRE_DIRECT:
				// Perform unmount
				log.Printf("Unmounting %v\n", packet.Name[:packet.NameLen])
				err = syscall.Unmount(mountPoint, 0)
				notifyCmd := AUTOFS_DEV_IOCTL_READY_CMD
				if err != nil {
					log.Printf("failed to unmount %s: %v\n", mountPoint, err)
					notifyCmd = AUTOFS_DEV_IOCTL_FAIL_CMD
				}
				_, _, err = a.ioctl(ioctlfd, notifyCmd, uint32(packet.WaitQueueToken), 0)
				if err != nil {
					log.Printf("failed to signal ready: %v\n", err)
					return
				}
			default:
				log.Printf("Unknown packet type: %d\n", packet.Type)
			}
		}
	}()
	return nil
}

func (a *AutoFS) openIoctlFd(mountPoint string) (int32, error) {
	st, err := os.Stat(mountPoint)
	if err != nil {
		return 0, fmt.Errorf("failed to stat %s: %w", mountPoint, err)
	}
	totalSize := unsafe.Sizeof(autofsDevIoctl{}) + uintptr(len(mountPoint)) + 1
	param := autofsDevIoctlWithPath{
		autofsDevIoctl: autofsDevIoctl{
			VerMajor: AUTOFS_DEV_IOCTL_VERSION_MAJOR,
			VerMinor: AUTOFS_DEV_IOCTL_VERSION_MINOR,
			Size:     uint32(totalSize),
			IoctlFd:  -int32(syscall.EBADF),
			Args1:    uint32(st.Sys().(*syscall.Stat_t).Dev),
		},
	}
	copy(param.Path[:], mountPoint)
	log.Printf("param.path: %v\n", param.Path)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(a.devFd), uintptr(AUTOFS_DEV_IOCTL_OPENMOUNT_CMD), uintptr(unsafe.Pointer(&param)))
	if errno != 0 {
		return 0, fmt.Errorf("failed to open mountpoint %s: %d", mountPoint, errno)
	}
	return param.IoctlFd, nil
}

func (a *AutoFS) ioctl(ioctlfd int32, command autofsIoctlCommand, arg1 uint32, arg2 uint32) (arg1o, arg2o uint32, err error) {
	param := autofsDevIoctl{
		VerMajor: AUTOFS_DEV_IOCTL_VERSION_MAJOR,
		VerMinor: AUTOFS_DEV_IOCTL_VERSION_MINOR,
		Size:     uint32(ioctlSize),
		IoctlFd:  int32(ioctlfd),
		Args1:    arg1,
		Args2:    arg2,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(a.devFd), uintptr(command), uintptr(unsafe.Pointer(&param)))
	if errno != 0 {
		return 0, 0, fmt.Errorf("failed to ioctl: %d", errno)
	}
	return param.Args1, param.Args2, nil
}

func (a *AutoFS) Run() error {

	fstab, err := ParseFstab("/etc/fstab")
	if err != nil {
		return fmt.Errorf("failed to parse fstab: %w", err)
	}
	for _, entry := range fstab {
		err = a.Mount(entry.MountPoint, func(mountpoint string) error {
			// Run command "mount mountpoint"

			err := exec.Command("mount", mountpoint).Run()
			if err != nil {
				return fmt.Errorf("failed to mount %s: %w", mountpoint, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to setup auto-mount %s: %w", entry.MountPoint, err)
		}
	}
	return nil
}

type AutoFsDaemonPlugin struct {
	PluginBase
}

func NewAutoFsDaemonPlugin() *AutoFsDaemonPlugin {
	return &AutoFsDaemonPlugin{}
}

func (p *AutoFsDaemonPlugin) Run(ctx context.Context) error {
	autofs, err := NewAutoFS()
	if err != nil {
		return fmt.Errorf("failed to create autofs: %w", err)
	}
	return p.RunNext(context.WithValue(ctx, p, autofs))
}

func (p *AutoFsDaemonPlugin) GetMountPlugin() *AutoFsMountPlugin {
	return &AutoFsMountPlugin{daemonPlugin: p}
}

type AutoFsMountPlugin struct {
	PluginBase
	daemonPlugin *AutoFsDaemonPlugin
}

func (p *AutoFsMountPlugin) Run(ctx context.Context) error {
	daemon := ctx.Value(p.daemonPlugin).(*AutoFS)
	err := daemon.Run()
	if err != nil {
		return fmt.Errorf("Failed to run auto-mount %w", err)
	}
	return p.RunNext(ctx)
}
