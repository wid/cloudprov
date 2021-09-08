/*
Copyright 2017 The Kubernetes Authors.

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

type PostgresProvisionningPhase string

const (
	PostgresProvisionningPending   PostgresProvisionningPhase = "Pending"
	PostgresProvisionningRunning   PostgresProvisionningPhase = "Running"
	PostgresProvisionningSucceeded PostgresProvisionningPhase = "Succeeded"
	PostgresProvisionningFailed    PostgresProvisionningPhase = "Failed"
	PostgresProvisionningUnknown   PostgresProvisionningPhase = "Unknown"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Postgres is a specification for a Postgres resource
type Postgres struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresSpec   `json:"spec"`
	Status PostgresStatus `json:"status"`
}

// PostgresSpec is the spec for a Postgres resource
type PostgresSpec struct {
	DatabaseName string `json:"databaseName"`
}

// PostgresStatus is the status for a Postgres resource
type PostgresStatus struct {
	// The number of actively running pods.
	// +optional
	ProvisioningStatus PostgresProvisionningPhase `json:"provisioningStatus,omitempty"`
	DatabaseName       string                     `json:"databaseName,omitempty"`
	UserName           string                     `json:"userName,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PostgresList is a list of Postgres resources
type PostgresList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Postgres `json:"items"`
}
