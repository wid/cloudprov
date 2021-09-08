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

package main

import (
	"encoding/json"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudprovv1alpha1 "cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
)

func newDatabaseCreationJob(postgres *cloudprovv1alpha1.Postgres, databaseCredentials DatabaseCredentials, config Config) *batchv1.Job {
	mode := int32(493) // 493d == 0755o
	backoffLimit := int32(2)
	labels := map[string]string{
		"app":                "postgres-provisionner",
		"postgres-name":      postgres.Name,
		"postgres-namespace": postgres.Namespace,
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      creationJobName(postgres),
			Namespace: config.JobNamespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(config.SelfPod, corev1.SchemeGroupVersion.WithKind("Pod")),
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &config.TTLSecondsAfterFinished32,
			BackoffLimit:            &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "postgres-provisionner",
							Image:   "widmaster/postgres-provisionner",
							Command: []string{"/bin/sh", "-c", "/cloudprov-scripts/create-init-database"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "cloudprov-scripts",
									MountPath: "/cloudprov-scripts",
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: config.AdminSecret,
										},
									},
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "PGUSER_CREATE",
									Value: databaseCredentials.PGUSER_CREATE,
								},
								{
									Name:  "PGPASSWORD_CREATE",
									Value: databaseCredentials.PGPASSWORD_CREATE,
								},
								{
									Name:  "PGDATABASE_CREATE",
									Value: databaseCredentials.PGDATABASE_CREATE,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "cloudprov-scripts",
							VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "cloudprov-scripts",
								},
								DefaultMode: &mode,
							},
							},
						},
					},
				},
			},
		},
	}
}

func newDatabaseDeletionJob(postgres *cloudprovv1alpha1.Postgres, databaseCredentials *DatabaseCredentials, config Config) *batchv1.Job {
	mode := int32(493) // 493d == 0755o
	backoffLimit := int32(2)

	labels := map[string]string{
		"app":                "postgres-provisionner",
		"postgres-name":      postgres.Name,
		"postgres-namespace": postgres.Namespace,
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deletionJobName(postgres),
			Namespace: config.JobNamespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(config.SelfPod, corev1.SchemeGroupVersion.WithKind("Pod")),
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &config.TTLSecondsAfterFinished32,
			BackoffLimit:            &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "postgres-provisionner",
							Image:   "widmaster/postgres-provisionner",
							Command: []string{"/bin/sh", "-c", "/cloudprov-scripts/delete-database"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "cloudprov-scripts",
									MountPath: "/cloudprov-scripts",
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: config.AdminSecret,
										},
									},
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "PGUSER_DELETE",
									Value: databaseCredentials.PGUSER,
								},
								{
									Name:  "PGDATABASE_DELETE",
									Value: databaseCredentials.PGDATABASE,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "cloudprov-scripts",
							VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "cloudprov-scripts",
								},
								DefaultMode: &mode,
							},
							},
						},
					},
				},
			},
		},
	}
}

func newAuthSecret(postgres *cloudprovv1alpha1.Postgres, adminSecret *corev1.Secret) (*corev1.Secret, error) {
	var authSecret corev1.Secret
	var createdDatabaseCredentials DatabaseCredentials
	authSecret.ObjectMeta.Name = postgres.Name
	authSecret.ObjectMeta.Namespace = postgres.Namespace
	authSecret.ObjectMeta.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(postgres, cloudprovv1alpha1.SchemeGroupVersion.WithKind("Postgres")),
	}

	authSecret.Data = make(map[string][]byte)

	if adminSecret.Data[referenceName(postgres)] == nil {
		return nil, fmt.Errorf("Could not find %v entry, in adminSecret", referenceName(postgres))
	}

	err := json.Unmarshal(adminSecret.Data[referenceName(postgres)], &createdDatabaseCredentials)
	if err != nil {
		DebugF("%v\n", err)
		return nil, err
	}

	authSecret.Data["PGDATABASE"] = []byte(createdDatabaseCredentials.PGDATABASE)
	authSecret.Data["PGHOST"] = []byte(createdDatabaseCredentials.PGHOST)
	authSecret.Data["PGPASSWORD"] = []byte(createdDatabaseCredentials.PGPASSWORD)
	authSecret.Data["PGPORT"] = []byte(createdDatabaseCredentials.PGPORT)
	authSecret.Data["PGSSLMODE"] = []byte(createdDatabaseCredentials.PGSSLMODE)
	authSecret.Data["PGUSER"] = []byte(createdDatabaseCredentials.PGUSER)
	authSecret.Data["POSTGRES_SSL"] = []byte(createdDatabaseCredentials.POSTGRES_SSL)

	return &authSecret, nil
}
