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

// +kubebuilder:object:generate=true
// +groupName=velda.io
// +versionName=v1
package k8s

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentPoolAutoScalerSpec struct {
	MaxReplicas int `json:"maxReplicas"`
	MinIdle     int `json:"minIdle"`
	MaxIdle     int `json:"maxIdle"`
	// +kubebuilder:default:=60
	IdleDecay int `json:"idleDecaySecond"`
	// +kubebuilder:default:=300
	KillUnknownAfter int `json:"killUnknownAfterSecond"`
	// +kubebuilder:default:=1
	DefaultSlotsPerAgent int `json:"defaultSlotsPerAgent,omitempty"`
}

type AgentPoolSpec struct {
	AutoScaler AgentPoolAutoScalerSpec `json:"autoscaler,omitempty"`
	Template   corev1.Pod              `json:"template"`
}

// +groupName=velda.io
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=ap,categories=all
type AgentPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentPoolSpec `json:"spec,omitempty"`
}

type AgentPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPool `json:"items"`
}
