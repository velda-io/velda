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
package clientlib

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"velda.io/velda/pkg/proto"
)

const (
	FallbackToSession = true
)

var (
	brokerAddrFlag string
	once           sync.Once
	conn           *grpc.ClientConn
	connErr        error
)

func GetBrokerClient() (proto.BrokerServiceClient, error) {
	conn, connErr := GetApiConnection()
	if connErr != nil {
		return nil, connErr
	}
	return proto.NewBrokerServiceClient(conn), nil
}

func GetApiConnection() (*grpc.ClientConn, error) {
	once.Do(func() {
		var brokerAddr string
		if agentConfig == nil {
			cfg, err := CurrentConfig()
			if err != nil {
				connErr = err
				return
			}
			brokerAddr, connErr = cfg.GetConfig("broker")
			if connErr != nil {
				return
			}
		} else {
			brokerAddr = brokerAddrFlag
		}
		if brokerAddr == "" {
			err := errors.New("broker address not set")
			connErr = err
			return
		}
		var brokerUrl *url.URL
		if strings.HasPrefix(brokerAddr, "http://") ||
			strings.HasPrefix(brokerAddr, "https://") {
			brokerUrl, connErr = url.Parse(brokerAddr)
		} else {
			brokerUrl, connErr = url.Parse("http://" + brokerAddr)
		}
		if connErr != nil {
			return
		}

		var credential credentials.TransportCredentials
		if brokerUrl.Scheme == "https" {
			credential = credentials.NewTLS(nil)
		} else {
			credential = insecure.NewCredentials()
		}
		// Port needs to be explicitly set for GRPC DNS resolver.
		port := brokerUrl.Port()
		if port == "" {
			if brokerUrl.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		conn, connErr = grpc.NewClient(
			"dns:"+brokerUrl.Hostname()+":"+port,
			grpc.WithTransportCredentials(credential),
			grpc.WithAuthority(brokerUrl.Host),
			grpc.WithUnaryInterceptor(GetAuthInterceptor()))
		if connErr != nil {
			return
		}
	})
	return conn, connErr
}

func GetInstanceIdFromServer(instanceName string) (instanceId int64, err error) {
	conn, err := GetApiConnection()
	if err != nil {
		return 0, fmt.Errorf("Error getting API connection: %v", err)
	}
	instanceServer := proto.NewInstanceServiceClient(conn)
	instance, err := instanceServer.GetInstanceByName(
		context.Background(), &proto.GetInstanceByNameRequest{
			InstanceName: instanceName,
		})
	if err != nil {
		return 0, fmt.Errorf("Error looking up instance: %v", err)
	}
	return instance.Id, nil
}

func ParseInstanceId(ctx context.Context, instance string, fallbackToSession bool) (int64, error) {
	var instanceId int64
	if instance != "" {
		var err error
		// If instance is not int, convert it to int.
		instanceId, err = strconv.ParseInt(instance, 10, 64)
		if err != nil {
			instanceId, err = GetInstanceIdFromServer(instance)
			if err != nil {
				return 0, err
			}
		}
	} else if fallbackToSession && IsInSession() {
		instanceId = agentConfig.Instance
	} else if !IsInSession() {
		currentCfg, err := CurrentConfig()
		if err == nil {
			defaultInstance, err := currentCfg.GetConfig("default-instance")
			if err == nil {
				parts := strings.Split(defaultInstance, ":")
				instanceId, err = strconv.ParseInt(parts[0], 10, 64)
			}
		}
	}
	if instanceId == 0 {
		return 0, errors.New("Instance not set")
	}
	return instanceId, nil
}
