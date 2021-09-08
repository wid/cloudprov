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
	"context"
	"encoding/json"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
	cloudprovv1alpha1 "cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
)

/////////////////////////////////////////////////////
// Postgres

func (c *Controller) updatePostgresStatusFromSecret(postgres *cloudprovv1alpha1.Postgres, postgresSecret *corev1.Secret) (*cloudprovv1alpha1.Postgres, error) {
	DebugF("updatePostgresStatusFromSecret\n")
	postgresUpdate := postgres.DeepCopy()
	postgresUpdate.Status.ProvisioningStatus = cloudprovv1alpha1.PostgresProvisionningSucceeded
	postgresUpdate.Status.DatabaseName = string(postgresSecret.Data["PGDATABASE"])
	postgresUpdate.Status.UserName = string(postgresSecret.Data["PGUSER"])
	return c.cloudprovclientset.SamplecontrollerV1alpha1().Postgreses(postgres.Namespace).Update(context.TODO(), postgresUpdate, metav1.UpdateOptions{})
}

func (c *Controller) updatePostgresStatusFromJob(postgres *cloudprovv1alpha1.Postgres, job *batchv1.Job) (*cloudprovv1alpha1.Postgres, error) {
	postgresCopy := postgres.DeepCopy()
	if job.Status.Active == 1 {
		postgresCopy.Status.ProvisioningStatus = cloudprovv1alpha1.PostgresProvisionningRunning
	}
	if job.Status.Failed == 1 {
		postgresCopy.Status.ProvisioningStatus = cloudprovv1alpha1.PostgresProvisionningFailed
	}
	if job.Status.Succeeded == 1 {
		postgresCopy.Status.ProvisioningStatus = cloudprovv1alpha1.PostgresProvisionningSucceeded
	}
	return c.cloudprovclientset.SamplecontrollerV1alpha1().Postgreses(postgres.Namespace).Update(context.TODO(), postgresCopy, metav1.UpdateOptions{})
}

func (c *Controller) updateDeletionFinalizer(postgres *cloudprovv1alpha1.Postgres) error {
	postgresCopy := postgres.DeepCopy()

	for i, v := range postgresCopy.ObjectMeta.Finalizers {
		if v == "cloudprov.org/delete-database" {
			postgresCopy.ObjectMeta.Finalizers = append(postgresCopy.ObjectMeta.Finalizers[:i], postgresCopy.ObjectMeta.Finalizers[i+1:]...)
			break
		}
	}
	_, err := c.cloudprovclientset.SamplecontrollerV1alpha1().Postgreses(postgres.Namespace).Update(context.TODO(), postgresCopy, metav1.UpdateOptions{})
	return err
}

/////////////////////////////////////////////////////
// Secret

func (c *Controller) getPostgresSecret(postgres *cloudprovv1alpha1.Postgres) (*corev1.Secret, error) {
	secret, err := c.kubeclientset.CoreV1().Secrets(postgres.Namespace).Get(context.TODO(), postgres.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func (c *Controller) getAdminSecret(postgres *cloudprovv1alpha1.Postgres) (*corev1.Secret, error) {

	adminSecret, err := c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Get(context.TODO(), config.AdminSecret, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return adminSecret, nil
}

func (c *Controller) updateAdminSecret(adminSecret *corev1.Secret) (*corev1.Secret, error) {
	return c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Update(context.TODO(), adminSecret, metav1.UpdateOptions{})
}

func (c *Controller) createPostgresSecret(postgres *v1alpha1.Postgres) (*corev1.Secret, error) {
	adminSecret, err := c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Get(context.TODO(), config.AdminSecret, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	authSecret, err := newAuthSecret(postgres, adminSecret)
	if err != nil {
		return nil, err
	}
	secret, err := c.kubeclientset.CoreV1().Secrets(postgres.Namespace).Create(context.TODO(), authSecret, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func (c *Controller) removeFromAdminSecret(objectRef string, credentials map[string][]byte) error {

	adminSecret, err := c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Get(context.TODO(), config.AdminSecret, metav1.GetOptions{})
	delete(adminSecret.Data, objectRef)
	if err != nil {
		return err
	}
	adminSecret, err = c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Update(context.TODO(), adminSecret, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return nil
}

/////////////////////////////////////////////////////
// DatabaseCredentials

func (c *Controller) getDatabaseSecretFromAdmin(postgres cloudprovv1alpha1.Postgres) (*DatabaseCredentials, error) {
	databaseCredentials, err := c.getDatabaseCredentialsFromAdminSecret(&postgres, config)
	if err != nil {
		Infof("Couldn't find database in admin-secret, deleting anyway")
	}
	return databaseCredentials, err
}

func (c *Controller) getDatabaseCredentialsFromAdminSecret(postgres *cloudprovv1alpha1.Postgres, config Config) (*DatabaseCredentials, error) {
	var databaseCredentials DatabaseCredentials
	adminSecret, err := c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Get(context.TODO(), config.AdminSecret, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if databaseCredentialsJson, ok := adminSecret.Data[referenceName(postgres)]; !ok {
		return nil, fmt.Errorf("No entry found for %v", referenceName(postgres))
	} else {
		err = json.Unmarshal(databaseCredentialsJson, &databaseCredentials)
		if err != nil {
			return nil, err
		}
		return &databaseCredentials, nil
	}
}

/////////////////////////////////////////////////////
// Job

func (c *Controller) createDatabaseCreationJob(postgres *cloudprovv1alpha1.Postgres, config Config) (*batchv1.Job, error) {
	var authSecret DatabaseCredentials
	adminSecret, err := c.kubeclientset.CoreV1().Secrets(config.JobNamespace).Get(context.TODO(), config.AdminSecret, metav1.GetOptions{})
	if err != nil {
		Infof("Error getting Secret %v in namespace %v: %v", config.AdminSecret, config.JobNamespace, err)
		return nil, err
	}
	authSecretJson := adminSecret.Data[referenceName(postgres)]
	json.Unmarshal(authSecretJson, &authSecret)
	job, err := c.kubeclientset.BatchV1().Jobs(config.JobNamespace).Create(context.TODO(), newDatabaseCreationJob(postgres, authSecret, config), metav1.CreateOptions{})
	return job, err
}

func (c *Controller) createDatabaseDeletionJob(postgres cloudprovv1alpha1.Postgres, databaseCredentials *DatabaseCredentials) (*batchv1.Job, error) {
	job, err := c.kubeclientset.BatchV1().Jobs(config.JobNamespace).Create(context.TODO(), newDatabaseDeletionJob(&postgres, databaseCredentials, config), metav1.CreateOptions{})
	if err != nil {
		Infof("Error creating Job %v in namespace %v: %v", postgres.Name, config.JobNamespace, err)
		return nil, err
	}
	return job, nil
}

func (c *Controller) getDeletionJob(postgres *v1alpha1.Postgres) (*batchv1.Job, error) {
	deletionJob, err := c.jobsLister.Jobs(c.config.JobNamespace).Get(deletionJobName(postgres))
	return deletionJob, err
}
