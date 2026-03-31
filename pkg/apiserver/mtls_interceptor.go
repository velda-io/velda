package apiserver

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	configpb "velda.io/velda/pkg/proto/config"
)

const agentUpdateFullMethod = "/velda.BrokerService/AgentUpdate"

type MTLSMethodCNMap map[string]string

type mtlsVerifier struct {
	methodCN      map[string]string
	methodMatcher map[string]*regexp.Regexp
}

func ProvideMTLSMethodCNMap(cfg *configpb.Config) MTLSMethodCNMap {
	result := MTLSMethodCNMap{}
	if cfg == nil || cfg.GetServer() == nil {
		return result
	}
	cn := strings.TrimSpace(cfg.GetServer().GetCertCn())
	if cn != "" {
		result[agentUpdateFullMethod] = cn
	}
	return result
}

func ProvideMTLSVerifier(methodCN MTLSMethodCNMap) *mtlsVerifier {
	v := &mtlsVerifier{
		methodCN:      map[string]string{},
		methodMatcher: map[string]*regexp.Regexp{},
	}
	for method, cn := range methodCN {
		trimmedCN := strings.TrimSpace(cn)
		if method == "" || trimmedCN == "" {
			continue
		}
		v.methodCN[method] = trimmedCN
		pattern := `(^|[^[:alnum:]_])` + regexp.QuoteMeta(trimmedCN) + `([^[:alnum:]_]|$)`
		v.methodMatcher[method] = regexp.MustCompile(pattern)
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
	requiredCN, ok := v.methodCN[method]
	if !ok || requiredCN == "" {
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
	cn := extractCNFromForwardedClientCert(xfcc[0])
	if cn == "" {
		return errors.New("x-forwarded-client-cert Subject CN is required")
	}
	matcher := v.methodMatcher[method]
	if matcher == nil || !matcher.MatchString(cn) {
		return errors.New("x-forwarded-client-cert Subject CN does not match required CN")
	}
	return nil
}

func extractCNFromForwardedClientCert(xfcc string) string {
	var subject string
	for _, part := range strings.Split(xfcc, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), "subject") {
			subject = strings.TrimSpace(strings.Trim(kv[1], `"`))
			break
		}
	}
	if subject == "" {
		return ""
	}
	for _, attr := range strings.Split(subject, ",") {
		kv := strings.SplitN(strings.TrimSpace(attr), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), "CN") {
			return strings.TrimSpace(strings.Trim(kv[1], `"`))
		}
	}
	match := regexp.MustCompile(`(?:^|/)CN=([^/,]+)`).FindStringSubmatch(subject)
	if len(match) == 2 {
		return strings.TrimSpace(strings.Trim(match[1], `"`))
	}
	return ""
}
