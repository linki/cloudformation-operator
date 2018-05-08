package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type StackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Stack `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Stack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              StackSpec   `json:"spec"`
	Status            StackStatus `json:"status,omitempty"`
}

type StackSpec struct {
	Template   string            `json:"template"`
	Parameters map[string]string `json:"parameters"`
}

// StackStatus is the status for a Stack resource
type StackStatus struct {
	StackID string            `json:"stackID"`
	Outputs map[string]string `json:"outputs"`
}
