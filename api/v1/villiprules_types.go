/*
Copyright 2025.

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

package v1

import (
	villipFilter "github.com/marema31/villip/filter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VillipRulesSpec defines the desired state of VillipRules.
type VillipRulesSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of VillipRules. Edit villiprules_types.go to remove/update
	ContentTypes []string                    `yaml:"content-types" json:"content-types,omitempty"` //nolint: tagliatelle
	Dump         villipFilter.Cdump          `yaml:"dump" json:"dump,omitempty"`
	Force        bool                        `yaml:"force" json:"force,omitempty"`
	Insecure     bool                        `yaml:"insecure" json:"insecure,omitempty"`
	Port         int                         `yaml:"port" json:"port,omitempty"`
	Prefix       []villipFilter.Creplacement `yaml:"prefix" json:"prefix,omitempty"`
	Priority     uint8                       `yaml:"priority" json:"priority,omitempty"`
	Replace      []villipFilter.Creplacement `yaml:"replace" json:"replace,omitempty"`
	Request      villipFilter.Caction        `yaml:"request" json:"request,omitempty"`
	Response     villipFilter.Caction        `yaml:"response" json:"response,omitempty"`
	Restricted   []string                    `yaml:"restricted" json:"restricted,omitempty"`
	Status       []string                    `yaml:"status" json:"status,omitempty"`
	Token        []villipFilter.CtokenAction `yaml:"token" json:"token,omitempty"`
	Type         string                      `yaml:"type" json:"type,omitempty"`
	URL          string                      `yaml:"url" json:"url,omitempty"`
}

// VillipRulesStatus defines the observed state of VillipRules.
type VillipRulesStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// VillipRules is the Schema for the villiprules API.
type VillipRules struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VillipRulesSpec   `json:"spec,omitempty"`
	Status VillipRulesStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VillipRulesList contains a list of VillipRules.
type VillipRulesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VillipRules `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VillipRules{}, &VillipRulesList{})
}
