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

type FieldForgeSpecReference struct {
	// The type of value reference, must be on of "constant", "configMap", "secret"
	Type string `json:"type,omitempty"`

	ConfigMap ObjectValue   `json:"configMap,omitempty"`
	Secret    ObjectValue   `json:"secret,omitempty"`
	Constant  ConstantValue `json:"constant,omitempty"`
}

func (c FieldForgeSpecReference) GetType() string {
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

// FieldForgeSpec defines the desired state of FieldForge
type FieldForgeSpec struct {
	// The target configmap or secret to make/maintain
	Target TargetSpec `json:"target"`

	// Annotations to go in target object
	Annotations map[string]FieldForgeSpecReference `json:"annotations,omitempty"`
	// Labels to go in target object
	Labels map[string]FieldForgeSpecReference `json:"labels,omitempty"`

	StringData map[string]FieldForgeSpecReference `json:"stringData,omitempty"`
	BinaryData map[string]FieldForgeSpecReference `json:"binaryData,omitempty"`
}

// FieldForgeStatus defines the observed state of FieldForge
type FieldForgeStatus struct {
	// Conditions store the status conditions of the FieldForge instances
	// +operator-sdk:csv:customresourcedefinitions:type=status
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// FieldForge is the Schema for the fieldforges API
type FieldForge struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FieldForgeSpec   `json:"spec,omitempty"`
	Status FieldForgeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FieldForgeList contains a list of FieldForge
type FieldForgeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FieldForge `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FieldForge{}, &FieldForgeList{})
}
