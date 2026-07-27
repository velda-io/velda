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
package utils

import (
	"errors"
	"fmt"
	"net"
	"time"
)

func DetectReportIPv4(deviceName string) (net.IP, error) {
	if deviceName != "" {
		return DetectInterfaceIPv4(deviceName)
	}
	return DetectDefaultPublicIPv4()
}

func DetectInterfaceIPv4(deviceName string) (net.IP, error) {
	iface, err := net.InterfaceByName(deviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to find interface %q: %w", deviceName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to list addresses for interface %q: %w", deviceName, err)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() {
			continue
		}
		return ip, nil
	}
	return nil, fmt.Errorf("no usable IPv4 address found on interface %q", deviceName)
}

func DetectDefaultPublicIPv4() (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			dialer := &net.Dialer{
				Timeout:   time.Second,
				LocalAddr: &net.UDPAddr{IP: ip, Port: 0},
			}
			conn, err := dialer.Dial("udp", "8.8.8.8:53")
			if err == nil {
				_ = conn.Close()
				return ip, nil
			}
		}
	}

	return nil, errors.New("no interface with public connectivity found")
}
