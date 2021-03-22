/*
MIT License

Copyright (c) 2018 Martin Linkhorst

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.
// Important: Run "make" to regenerate code after modifying this file

// Defines the desired state of Stack
type StackSpec struct {
	// +kubebuilder:validation:Optional
	Parameters map[string]string `json:"parameters,omitempty"`
	// +kubebuilder:validation:Optional
	Tags     map[string]string `json:"tags,omitempty"`
	Template string            `json:"template"`
}

// Defines the observed state of Stack
type StackStatus struct {
	StackID string `json:"stackID"`
	// +kubebuilder:validation:Optional
	StackStatus string `json:"stackStatus"`
	// +kubebuilder:validation:Optional
	// +nullable
	CreatedTime metav1.Time `json:"createdTime,omitEmpty"`
	// +kubebuilder:validation:Optional
	// +nullable
	UpdatedTime metav1.Time `json:"updatedTime,omitEmpty"`
	// +kubebuilder:validation:Optional
	// +nullable
	Outputs map[string]string `json:"outputs,omitEmpty"`
	// +kubebuilder:validation:Optional
	// +nullable
	Resources []StackResource `json:"resources,omitEmpty"`
}

// Defines a resource provided/managed by a Stack and its current state
type StackResource struct {
	LogicalId  string `json:"logicalID"`
	PhysicalId string `json:"physicalID"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	// +kubebuilder:validation:Optional
	StatusReason string `json:"statusReason,omitEmpty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Stack is the Schema for the stacks API
type Stack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StackSpec   `json:"spec,omitempty"`
	Status StackStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StackList contains a list of Stack
type StackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Stack `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Stack{}, &StackList{})
}
