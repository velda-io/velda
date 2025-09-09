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
/*
Shared printing utilities for proto messages (json/yaml/table) and a simple
dot-path extractor for selecting fields as columns.
*/
package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// fieldSpec represents a column: Name to print and JSON path to extract.
type fieldSpec struct {
	Name string
	Path string
}

// PrintListOutput prints either a full proto message (for json/yaml) or a typed list (for table).
// Parameters:
// - full: if non-nil, used for json/yaml printing; may also be a TaskPageResult for tabular printing.
// - list: if non-nil and is a []*pproto.Task, used for tabular printing.
// - header: show header row for table
// - fieldsSpec: "json"|"yaml"|comma-separated list of fields; each item can be "name=path" or just "path".
func PrintListOutput(full proto.Message, list []proto.Message, header bool, fieldsSpec string, w io.Writer) error {
	// If requested json/yaml and a full message is provided, print full.
	if fieldsSpec == "json" || fieldsSpec == "yaml" {
		return printFullMessage(full, fieldsSpec, w)
	}

	// Parse field specs: if provided use them, otherwise use defaultFields
	var specs []fieldSpec
	if strings.TrimSpace(fieldsSpec) != "" {
		parts := strings.Split(fieldsSpec, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if strings.Contains(p, "=") {
				kv := strings.SplitN(p, "=", 2)
				specs = append(specs, fieldSpec{Name: strings.TrimSpace(kv[0]), Path: strings.TrimSpace(kv[1])})
			} else {
				// name derive from last fragment
				name := pathBase(p)
				specs = append(specs, fieldSpec{Name: name, Path: p})
			}
		}
	}

	// Print table header
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if header {
		// header: ID + column names
		cols := append([]string{}, columnNames(specs)...)
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}

	// For each task, extract fields
	for _, item := range list {
		b, err := protojson.Marshal(item)
		if err != nil {
			return err
		}
		var obj interface{}
		if err := json.Unmarshal(b, &obj); err != nil {
			return err
		}
		cols := []string{}
		for _, sp := range specs {
			cols = append(cols, extractByPath(obj, sp.Path))
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}
	return tw.Flush()
}

func printFullMessage(msg proto.Message, outFmt string, w io.Writer) error {
	switch outFmt {
	case "json":
		b, err := protojson.Marshal(msg)
		if err != nil {
			return err
		}
		_, _ = w.Write(b)
		return nil
	case "yaml":
		return PrintProtoYaml(msg, w)
	default:
		return fmt.Errorf("unknown output format: %s", outFmt)
	}
}

func PrintProtoYaml(msg proto.Message, w io.Writer) error {
	b, err := protojson.Marshal(msg)
	if err != nil {
		return err
	}
	var obj interface{}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	y, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	_, _ = w.Write(y)
	return nil
}

func pathBase(p string) string {
	parts := strings.Split(p, ".")
	if len(parts) == 0 {
		return p
	}
	return strings.Title(parts[len(parts)-1])
}

func columnNames(specs []fieldSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

// extractByPath traverses a parsed JSON object using a dot-separated path.
func extractByPath(obj interface{}, path string) string {
	if path == "" {
		b, _ := json.Marshal(obj)
		return string(b)
	}
	parts := strings.Split(path, ".")
	cur := obj
	for _, p := range parts {
		switch v := cur.(type) {
		case map[string]interface{}:
			if vv, ok := v[p]; ok {
				cur = vv
				continue
			}
			return ""
		case []interface{}:
			idx := -1
			if i, err := strconv.Atoi(p); err == nil {
				idx = i
			}
			if idx >= 0 && idx < len(v) {
				cur = v[idx]
				continue
			}
			return ""
		default:
			return fmt.Sprintf("%v", cur)
		}
	}
	switch t := cur.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
