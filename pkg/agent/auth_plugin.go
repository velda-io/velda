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
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/ssh"
	"velda.io/velda/pkg/proto"
)

const (
	// Keys for external access
	authorizedKeysPath = "/.velda/authorized_keys"
	// Key for internal access(e.g. vrun)
	keyPath = "/.velda/velda_key.pub"
)

type AuthPlugin struct {
	PluginBase
	requestPlugin any
}

func (p *AuthPlugin) Run(ctx context.Context) error {
	instanceId := ctx.Value(p.requestPlugin).(*proto.SessionRequest).InstanceId
	if err := loadOrGenerateSshKey(instanceId); err != nil {
		return fmt.Errorf("failed to load or generate SSH key: %w", err)
	}
	return p.RunNext(context.WithValue(ctx, p, &keyBasedSshAuth{}))
}

func NewAuthPlugin(requestPlugin any) *AuthPlugin {
	return &AuthPlugin{requestPlugin: requestPlugin}
}

type keyBasedSshAuth struct {
}

func (a *keyBasedSshAuth) GetConfig() (*ssh.ServerConfig, error) {
	return &ssh.ServerConfig{
		NoClientAuth:         true,
		NoClientAuthCallback: a.noClientAuthCallback,
		PublicKeyCallback:    a.keyCallback,
	}, nil
}

func (a *keyBasedSshAuth) noClientAuthCallback(conn ssh.ConnMetadata) (*ssh.Permissions, error) {
	if _, err := os.Stat(authorizedKeysPath); os.IsNotExist(err) {
		log.Printf("/.velda/authorized_keys file does not exist, will allow all access")
		return nil, nil
	}
	return nil, fmt.Errorf("no client auth allowed")
}

func (p *keyBasedSshAuth) keyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	for _, keyFilePath := range []string{keyPath, authorizedKeysPath} {
		keyData, err := os.ReadFile(keyFilePath)
		if err != nil {
			continue
		}

		for len(keyData) > 0 {
			publicKey, _, _, rest, err := ssh.ParseAuthorizedKey(keyData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse authorized key: %w", err)
			}
			if bytes.Equal(publicKey.Marshal(), key.Marshal()) {
				return nil, nil // Key is authorized
			}
			keyData = rest
		}
	}
	return nil, fmt.Errorf("public key %s is not authorized", conn.User())
}

func loadOrGenerateSshKey(instanceId int64) error {
	// Generate / load a key pair at /.velda/velda_key
	// Both server & client will share the key given they're accessing the shared storage.
	keyPath := "/.velda/velda_key"
	pubKeyPath := keyPath + ".pub"

	// Check if the key file exists
	if _, err := os.Stat(keyPath); err == nil {
		// Load the existing public key
		keyData, err := os.ReadFile(pubKeyPath)
		if err != nil {
			return fmt.Errorf("failed to read public key file: %w", err)
		}

		_, comment, _, _, err := ssh.ParseAuthorizedKey(keyData)

		if err != nil {
			return fmt.Errorf("failed to parse public key: %w", err)
		}
		if comment != fmt.Sprintf("velda-%d", instanceId) {
			log.Printf("Public key comment does not match instance ID %d, regenerate key", instanceId)
		} else {
			return nil
		}
	}
	// Generate a new Ed25519 key pair
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("failed to generate new Ed25519 key: %w", err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("failed to create SSH public key: %w", err)
	}

	// Ensure the .velda directory exists
	if err := os.MkdirAll("/.velda", 0700); err != nil {
		return fmt.Errorf("failed to create .velda directory: %w", err)
	}

	// Save the private key to the file
	privateKeyPem, err := ssh.MarshalPrivateKey(privateKey, "ED25519 PRIVATE KEY")
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	privateKeyBytes := pem.EncodeToMemory(privateKeyPem)
	err = os.WriteFile(keyPath, privateKeyBytes, 0644)
	if err != nil {
		return fmt.Errorf("failed to save private key: %w", err)
	}

	publicKeyBytes := ssh.MarshalAuthorizedKey(sshPublicKey)
	// Add the instance ID to the public key comment
	publicKeyBytes = append(publicKeyBytes[:len(publicKeyBytes)-1], []byte(fmt.Sprintf(" velda-%d\n", instanceId))...)
	err = os.WriteFile(pubKeyPath, publicKeyBytes, 0644)
	if err != nil {
		return fmt.Errorf("failed to save public key: %w", err)
	}

	log.Printf("Generated new SSH key pair for instance %d for velda-inter-session", instanceId)
	return nil
}
