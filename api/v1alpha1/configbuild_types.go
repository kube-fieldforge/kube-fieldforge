/*
Copyright 2024 Patrick Uiterwijk <patrick@puiterwijk.org>.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConstantValue struct {
	Value string `json:"value"`
}

type ObjectValue struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type ConfigBuildSpecReference struct {
	// The type of value reference, must be on of "constant", "configMap", "secret"
	Type string `json:"type,omitempty"`

	ConfigMap ObjectValue   `json:"configMap,omitempty"`
	Secret    ObjectValue   `json:"secret,omitempty"`
	Constant  ConstantValue `json:"constant,omitempty"`
}

func (c ConfigBuildSpecReference) GetType() string {
	if c.Type != "" {
		return c.Type
	}
	if c.ConfigMap.Name != "" {
		return "configmap"
	}
	if c.Secret.Name != "" {
		return "secret"
	}
	if c.Constant.Value != "" {
		return "constant"
	}

	return ""
}

type TargetSpec struct {
	// The type of object to be created, one of "configmap" or "secret".
	Kind string `json:"kind"`
	// The name of the object to be created.
	Name string `json:"name"`
}

// ConfigBuildSpec defines the desired state of ConfigBuild
type ConfigBuildSpec struct {
	// The target configmap or secret to make/maintain
	Target TargetSpec `json:"target"`

	// Annotations to go in target object
	Annotations map[string]ConfigBuildSpecReference `json:"annotations,omitempty"`
	// Labels to go in target object
	Labels map[string]ConfigBuildSpecReference `json:"labels,omitempty"`

	StringData map[string]ConfigBuildSpecReference `json:"stringData,omitempty"`
	BinaryData map[string]ConfigBuildSpecReference `json:"binaryData,omitempty"`
}

// ConfigBuildStatus defines the observed state of ConfigBuild
type ConfigBuildStatus struct {
	// Conditions store the status conditions of the Memcached instances
	// +operator-sdk:csv:customresourcedefinitions:type=status
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ConfigBuild is the Schema for the configbuilds API
type ConfigBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigBuildSpec   `json:"spec,omitempty"`
	Status ConfigBuildStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConfigBuildList contains a list of ConfigBuild
type ConfigBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfigBuild `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConfigBuild{}, &ConfigBuildList{})
}
