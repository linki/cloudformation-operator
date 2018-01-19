package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Stack is a specification for a Stack resource
type Stack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StackSpec   `json:"spec"`
	Status StackStatus `json:"status"`
}

// StackSpec is the spec for a Stack resource
type StackSpec struct {
	Template   string            `json:"template"`
	Parameters map[string]string `json:"parameters"`
}

// StackStatus is the status for a Stack resource
type StackStatus struct {
	StackID string            `json:"stackID"`
	Outputs map[string]string `json:"outputs"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// StackList is a list of Stack resources
type StackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Stack `json:"items"`
}
