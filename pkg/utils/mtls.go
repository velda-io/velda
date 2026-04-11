package utils

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// LoadPEMFromPathOrHTTPS reads PEM content from a local file path or an HTTPS URL.
func LoadPEMFromPathOrHTTPS(source string) ([]byte, error) {
	parsed, err := url.Parse(source)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		if parsed.Scheme != "https" {
			return nil, fmt.Errorf("unsupported CA URL scheme %q", parsed.Scheme)
		}
		resp, err := http.Get(source)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s returned %s", source, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}

	return os.ReadFile(source)
}

// BuildMTLSClientTransportCredentials builds client transport credentials.
// If all inputs are empty, it returns insecure credentials for backward compatibility.
func BuildMTLSClientTransportCredentials(clientCertPath, clientKeyPath, serverRootCASource, serverSPIFFEID string) (credentials.TransportCredentials, error) {
	if clientCertPath == "" && clientKeyPath == "" && serverRootCASource == "" && serverSPIFFEID == "" {
		return insecure.NewCredentials(), nil
	}

	if clientCertPath == "" || clientKeyPath == "" || serverRootCASource == "" || serverSPIFFEID == "" {
		return nil, fmt.Errorf("mTLS requires client cert, client key, server root CA and server SPIFFE ID")
	}

	expectedServerID, err := spiffeid.FromString(serverSPIFFEID)
	if err != nil {
		return nil, fmt.Errorf("invalid server SPIFFE ID %q: %w", serverSPIFFEID, err)
	}

	clientSVID, err := x509svid.Load(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client SVID cert/key: %w", err)
	}

	caPem, err := LoadPEMFromPathOrHTTPS(serverRootCASource)
	if err != nil {
		return nil, fmt.Errorf("failed to read server root CA: %w", err)
	}
	bundle, err := x509bundle.Parse(expectedServerID.TrustDomain(), caPem)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server root CA at %s", serverRootCASource)
	}

	return credentials.NewTLS(tlsconfig.MTLSClientConfig(
		clientSVID,
		bundle,
		tlsconfig.AuthorizeID(expectedServerID),
	)), nil
}

// BuildMTLSServerTLSConfig builds a TLS config for a server requiring mTLS client cert auth.
// If all inputs are empty, it returns nil for backward compatibility.
func BuildMTLSServerTLSConfig(tlsCertPath, tlsKeyPath, clientRootCASource, requiredClientSPIFFEID string) (*tls.Config, error) {
	if tlsCertPath == "" && tlsKeyPath == "" && clientRootCASource == "" && requiredClientSPIFFEID == "" {
		return nil, nil
	}

	if tlsCertPath == "" || tlsKeyPath == "" || clientRootCASource == "" || requiredClientSPIFFEID == "" {
		return nil, fmt.Errorf("mTLS server requires tls cert, tls key, client root CA and required client SPIFFE ID")
	}

	expectedClientID, err := spiffeid.FromString(requiredClientSPIFFEID)
	if err != nil {
		return nil, fmt.Errorf("invalid required client SPIFFE ID %q: %w", requiredClientSPIFFEID, err)
	}

	serverSVID, err := x509svid.Load(tlsCertPath, tlsKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load server SVID cert/key: %w", err)
	}

	caPem, err := LoadPEMFromPathOrHTTPS(clientRootCASource)
	if err != nil {
		return nil, fmt.Errorf("failed to read client root CA: %w", err)
	}
	bundle, err := x509bundle.Parse(expectedClientID.TrustDomain(), caPem)
	if err != nil {
		return nil, fmt.Errorf("failed to parse client root CA at %s", clientRootCASource)
	}

	return tlsconfig.MTLSServerConfig(
		serverSVID,
		bundle,
		tlsconfig.AuthorizeID(expectedClientID),
	), nil
}
