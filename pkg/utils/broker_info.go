package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	agentpb "velda.io/velda/pkg/proto/agent"
	configpb "velda.io/velda/pkg/proto/config"
)

func getPublicIp(ctx context.Context) (net.IP, error) {
	// Get the interface with the default route
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %w", err)
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
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				dialer := &net.Dialer{
					Timeout:   1 * time.Second,
					LocalAddr: &net.UDPAddr{IP: ipNet.IP, Port: 0},
				}
				// Attempt to dial out to a public DNS server to check if this interface has internet access
				conn, err := dialer.DialContext(ctx, "udp", "8.8.8.8:53")
				if err == nil {
					conn.Close()
					return ipNet.IP, nil
				}
			}
		}
	}

	return nil, errors.New("no interface with default route found")
}

func GetDefaultBrokerInfo(ctx context.Context, config *configpb.Config) (*agentpb.BrokerInfo, error) {
	if config.DefaultBrokerInfo != nil {
		return config.DefaultBrokerInfo, nil
	}
	// Full address specified
	if !strings.HasPrefix(config.Server.GrpcAddress, ":") {
		return &agentpb.BrokerInfo{
			Address: config.Server.GrpcAddress,
		}, nil
	}
	// Guess a public IP address
	endpoint, err := getPublicIp(ctx)

	// Extract port from GrpcAddress
	addrParts := strings.Split(config.Server.GrpcAddress, ":")
	port := 50051 // default port
	if len(addrParts) > 1 && addrParts[len(addrParts)-1] != "" {
		_, err := fmt.Sscanf(addrParts[len(addrParts)-1], "%d", &port)
		if err != nil {
			return nil, fmt.Errorf("failed to parse gRPC port from address %s: %w", config.Server.GrpcAddress, err)
		}
	}
	if err != nil {
		return nil, err
	}
	return &agentpb.BrokerInfo{
		Address: fmt.Sprintf("%s:%d", endpoint.String(), port),
	}, nil
}
