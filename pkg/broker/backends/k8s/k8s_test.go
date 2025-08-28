//go:build k8s

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
package k8s

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/clientcmd"

	"velda.io/velda/pkg/broker/backends/backend_testing"
)

const agentTemplate = `
apiVersion: v1
kind: Pod
metadata:
  name: agent-unittest
  namespace: default
  labels:
    app: agent-unittest
spec:
  containers:
  - name: agent
    image: us-west1-docker.pkg.dev/skyworkstation/velda.io/velda/agent@sha256:7f9d7137527cd20702f4273322edb9928474296c904a931403583c6d4ad2ffa0
    securityContext:
      privileged: true
    volumeMounts:
      - name: workspace-volume
        mountPath: /tmp
      - name: agent-disk
        mountPath: /tmp/agentdisk
      - name: agent-config
        mountPath: /run/velda
  nodeSelector:
    cloud.google.com/gke-nodepool: cpu-1
  volumes:
    - name: workspace-volume
      emptyDir: {}
    - name: agent-disk
      nfs:
        server: 10.24.119.242
        path: /main
    - name: agent-config
      configMap:
        name: agent-config
`

const agentSelector = "app=agent-unittest"

func TestK8sBackend(t *testing.T) {
	homeDir, _ := os.UserHomeDir()
	// Load Kubernetes configuration (e.g., from ~/.kube/config)
	kubeconfig := filepath.Join(homeDir, ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	assert.NoError(t, err)

	decoder := yaml.NewYAMLToJSONDecoder(strings.NewReader(agentTemplate))
	pod := &corev1.Pod{}
	if !assert.NoError(t, decoder.Decode(pod)) {
		t.FailNow()
	}

	backend, err := NewK8sPoolBackend(config, pod, agentSelector)
	require.NoError(t, err)

	backend_testing.TestSimpleScaleUpDown(t, backend)
}
