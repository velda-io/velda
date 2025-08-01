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
package utils

import (
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

func LoadProto(configPath string, msg proto.Message) error {
	rawBytes, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("Failed to read configuration file: %v", err)
	}

	var yamlData map[string]interface{}
	if err := yaml.Unmarshal(rawBytes, &yamlData); err != nil {
		return fmt.Errorf("Failed to parse yaml: %v", err)
	}

	jsonBytes, err := json.Marshal(yamlData)
	if err != nil {
		return fmt.Errorf("Failed to convert yaml to json: %v, %+v", err, yamlData)
	}

	if err := protojson.Unmarshal(jsonBytes, msg); err != nil {
		return fmt.Errorf("Failed to unmarshal configuration: %v", err)
	}
	return nil
}

func SerializeToYaml(msg proto.Message) (string, error) {
	jsonBytes, err := protojson.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("Failed to marshal proto message: %v", err)
	}

	var yamlData map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &yamlData); err != nil {
		return "", fmt.Errorf("Failed to convert json to yaml: %v", err)
	}

	yamlBytes, err := yaml.Marshal(yamlData)
	if err != nil {
		return "", fmt.Errorf("Failed to marshal to yaml: %v", err)
	}

	return string(yamlBytes), nil
}
