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
package apiserver

import (
	"context"
	"errors"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	configpb "velda.io/velda/pkg/proto/config"
)

const agentUpdateFullMethod = "/velda.BrokerService/AgentUpdate"

type MTLSMethodSPIFFEMap map[string]string

type mtlsVerifier struct {
	methodSPIFFE map[string]spiffeid.ID
}

func ProvideMTLSMethodSPIFFEMap(cfg *configpb.Config) MTLSMethodSPIFFEMap {
	result := MTLSMethodSPIFFEMap{}
	if cfg == nil || cfg.GetServer() == nil {
		return result
	}
	spiffeID := strings.TrimSpace(cfg.GetServer().GetCertSpiffeId())
	if spiffeID != "" {
		result[agentUpdateFullMethod] = spiffeID
	}
	return result
}

func ProvideMTLSVerifier(methodSPIFFE MTLSMethodSPIFFEMap) *mtlsVerifier {
	v := &mtlsVerifier{
		methodSPIFFE: map[string]spiffeid.ID{},
	}
	for method, id := range methodSPIFFE {
		trimmed := strings.TrimSpace(id)
		if method == "" || trimmed == "" {
			continue
		}
		parsed, err := spiffeid.FromString(trimmed)
		if err != nil {
			continue
		}
		v.methodSPIFFE[method] = parsed
	}
	return v
}

func ProvideMTLSUnaryInterceptor(v *mtlsVerifier) ServerMtlsUnaryInterceptor {
	return ServerMtlsUnaryInterceptor(v.UnaryInterceptor())
}

func ProvideMTLSStreamInterceptor(v *mtlsVerifier) ServerMtlsStreamInterceptor {
	return ServerMtlsStreamInterceptor(v.StreamInterceptor())
}

func (v *mtlsVerifier) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := v.verifyMethod(ctx, info.FullMethod); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return handler(ctx, req)
	}
}

func (v *mtlsVerifier) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := v.verifyMethod(ss.Context(), info.FullMethod); err != nil {
			return status.Error(codes.PermissionDenied, err.Error())
		}
		return handler(srv, ss)
	}
}

func (v *mtlsVerifier) verifyMethod(ctx context.Context, method string) error {
	requiredID, ok := v.methodSPIFFE[method]
	if !ok {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return errors.New("missing metadata")
	}
	xfcc := md.Get("x-forwarded-client-cert")
	if len(xfcc) == 0 || strings.TrimSpace(xfcc[0]) == "" {
		return errors.New("x-forwarded-client-cert is required")
	}
	actualID, err := extractSPIFFEFromForwardedClientCert(xfcc[0])
	if err != nil {
		return err
	}
	if err := tlsconfig.AuthorizeID(requiredID)(actualID, nil); err != nil {
		return errors.New("x-forwarded-client-cert SPIFFE ID does not match required SPIFFE ID")
	}
	return nil
}

func extractSPIFFEFromForwardedClientCert(xfcc string) (spiffeid.ID, error) {
	var uriValue string
	for _, part := range strings.Split(xfcc, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), "uri") {
			uriValue = strings.TrimSpace(strings.Trim(kv[1], `"`))
			break
		}
	}
	if uriValue == "" {
		return spiffeid.ID{}, errors.New("x-forwarded-client-cert URI is required")
	}
	id, err := spiffeid.FromString(uriValue)
	if err != nil {
		return spiffeid.ID{}, errors.New("x-forwarded-client-cert URI is not a valid SPIFFE ID")
	}
	return id, nil
}
