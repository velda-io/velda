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
	"fmt"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	configpb "velda.io/velda/pkg/proto/config"
)

const (
	ServerRoleAgent    = "agent"
	ServerRoleAppProxy = "app_proxy"
)

type verifiedMTLSSPIFFEContextKey struct{}

func withVerifiedMTLSSPIFFE(ctx context.Context, spiffeID string) context.Context {
	return context.WithValue(ctx, verifiedMTLSSPIFFEContextKey{}, strings.TrimSpace(spiffeID))
}

func VerifiedMTLSSPIFFEIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(verifiedMTLSSPIFFEContextKey{}).(string)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

type MTLSEvidenceSource int

const (
	MTLSEvidenceSourceForwarded MTLSEvidenceSource = iota
	MTLSEvidenceSourcePeerTLS
)

type mtlsVerifier struct {
	source MTLSEvidenceSource
}

func ProvideMTLSEvidenceSource(cfg *configpb.Config) MTLSEvidenceSource {
	if cfg == nil || cfg.GetServer() == nil {
		return MTLSEvidenceSourceForwarded
	}
	switch cfg.GetServer().GetMtlsIdentitySource() {
	case configpb.Server_MTLS_IDENTITY_SOURCE_PEER_TLS_CERT:
		return MTLSEvidenceSourcePeerTLS
	default:
		return MTLSEvidenceSourceForwarded
	}
}

func ProvideMTLSVerifier(source MTLSEvidenceSource) *mtlsVerifier {
	v := &mtlsVerifier{
		source: source,
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
		spiffeID, verified, err := v.verifyIdentity(ctx)
		if err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		if verified {
			ctx = withVerifiedMTLSSPIFFE(ctx, spiffeID)
		}
		return handler(ctx, req)
	}
}

func (v *mtlsVerifier) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		spiffeID, verified, err := v.verifyIdentity(ss.Context())
		if err != nil {
			return status.Error(codes.PermissionDenied, err.Error())
		}
		if !verified {
			return handler(srv, ss)
		}
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          withVerifiedMTLSSPIFFE(ss.Context(), spiffeID),
		}
		return handler(srv, wrapped)
	}
}

type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

func (v *mtlsVerifier) verifyIdentity(ctx context.Context) (string, bool, error) {
	actualID, ok, err := v.extractSPIFFEFromContext(ctx)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	// Return the SPIFFE ID string; role lookup is handled at permission check time.
	return actualID.String(), true, nil
}

func (v *mtlsVerifier) extractSPIFFEFromContext(ctx context.Context) (spiffeid.ID, bool, error) {
	if v.source == MTLSEvidenceSourcePeerTLS {
		return extractSPIFFEFromPeerTLS(ctx)
	}

	md, hasMD := metadata.FromIncomingContext(ctx)
	if !hasMD {
		return spiffeid.ID{}, false, nil
	}
	xfcc := md.Get("x-forwarded-client-cert")
	if len(xfcc) == 0 || strings.TrimSpace(xfcc[0]) == "" {
		return spiffeid.ID{}, false, nil
	}
	id, err := extractSPIFFEFromForwardedClientCert(xfcc[0])
	if err != nil {
		return spiffeid.ID{}, false, err
	}
	return id, true, nil
}

func extractSPIFFEFromPeerTLS(ctx context.Context) (spiffeid.ID, bool, error) {
	pr, ok := peer.FromContext(ctx)
	if !ok || pr == nil {
		return spiffeid.ID{}, false, nil
	}
	tlsInfo, ok := pr.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return spiffeid.ID{}, false, nil
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return spiffeid.ID{}, false, nil
	}
	cert := tlsInfo.State.PeerCertificates[0]
	for _, uri := range cert.URIs {
		id, err := spiffeid.FromURI(uri)
		if err == nil {
			return id, true, nil
		}
	}
	return spiffeid.ID{}, false, errors.New("peer TLS certificate does not contain a valid SPIFFE URI SAN")
}

func splitSPIFFEIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
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
		return spiffeid.ID{}, fmt.Errorf("x-forwarded-client-cert URI is not a valid SPIFFE ID: %w", err)
	}
	return id, nil
}
