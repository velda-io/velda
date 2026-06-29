// Copyright 2026 Velda Inc
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
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

const (
	awsSecretScheme      = "awssecret"
	awsCurrentVersionTag = "AWSCURRENT"
)

type awsSecretRef struct {
	region  string
	name    string
	version string
}

// LoadFileOrSecret loads content from a local file path or an awssecret URI.
// Supported secret format: awssecret://region/name/version, where version is optional.
func LoadFileOrSecret(ctx context.Context, source string) ([]byte, error) {
	if strings.HasPrefix(source, awsSecretScheme+"://") {
		return loadAWSSecret(ctx, source)
	}
	return os.ReadFile(source)
}

func parseAWSSecretURI(secretURI string) (*awsSecretRef, error) {
	parsed, err := url.Parse(secretURI)
	if err != nil {
		return nil, fmt.Errorf("invalid awssecret URI %q: %w", secretURI, err)
	}
	if parsed.Scheme != awsSecretScheme {
		return nil, fmt.Errorf("unsupported secret scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("awssecret URI %q must include region", secretURI)
	}

	rawPath := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if rawPath == "" {
		return nil, fmt.Errorf("awssecret URI %q must include secret name", secretURI)
	}

	parts := strings.Split(rawPath, "/")
	decodedParts := make([]string, 0, len(parts))
	for i, part := range parts {
		if part == "" && i != len(parts)-1 {
			return nil, fmt.Errorf("awssecret URI %q contains empty path segment", secretURI)
		}
		decodedPart, err := url.PathUnescape(part)
		if err != nil {
			return nil, fmt.Errorf("awssecret URI %q has invalid escaped path segment: %w", secretURI, err)
		}
		decodedParts = append(decodedParts, decodedPart)
	}

	ref := &awsSecretRef{region: parsed.Host}
	if len(decodedParts) == 1 {
		ref.name = decodedParts[0]
		ref.version = awsCurrentVersionTag
	} else {
		ref.version = decodedParts[len(decodedParts)-1]
		ref.name = strings.Join(decodedParts[:len(decodedParts)-1], "/")
	}

	if ref.name == "" {
		return nil, fmt.Errorf("awssecret URI %q must include secret name", secretURI)
	}
	if ref.version == "" {
		ref.version = awsCurrentVersionTag
	}
	return ref, nil
}

func loadAWSSecret(ctx context.Context, secretURI string) ([]byte, error) {
	ref, err := parseAWSSecretURI(secretURI)
	if err != nil {
		return nil, err
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(ref.region))
	if err != nil {
		return nil, fmt.Errorf("failed loading AWS config for region %q: %w", ref.region, err)
	}

	client := secretsmanager.NewFromConfig(awsCfg)
	input := &secretsmanager.GetSecretValueInput{SecretId: aws.String(ref.name)}
	if strings.EqualFold(ref.version, "current") || ref.version == awsCurrentVersionTag {
		input.VersionStage = aws.String(awsCurrentVersionTag)
	} else {
		input.VersionId = aws.String(ref.version)
	}

	output, err := client.GetSecretValue(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed fetching secret %q in region %q: %w", ref.name, ref.region, err)
	}

	if output.SecretString != nil {
		return []byte(*output.SecretString), nil
	}
	if output.SecretBinary != nil {
		return output.SecretBinary, nil
	}
	return nil, fmt.Errorf("secret %q has no string or binary payload", ref.name)
}
