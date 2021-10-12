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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	batchinformers "k8s.io/client-go/informers/batch/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	batchlisters "k8s.io/client-go/listers/batch/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
	cloudprovv1alpha1 "cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
	clientset "cloudprov.org/cloudprov-controller/pkg/generated/clientset/versioned"
	cloudprovscheme "cloudprov.org/cloudprov-controller/pkg/generated/clientset/versioned/scheme"
	informers "cloudprov.org/cloudprov-controller/pkg/generated/informers/externalversions/cloudprovcontroller/v1alpha1"
	listers "cloudprov.org/cloudprov-controller/pkg/generated/listers/cloudprovcontroller/v1alpha1"
)

const controllerAgentName = "cloudprov-controller"

const (
	SuccessSynced         = "Synced"
	ErrResourceExists     = "ErrResourceExists"
	MessageResourceExists = "Resource %q already exists and is not managed by cloudprov"
	MessageResourceSynced = "Postgres synced successfully"
)

type DatabaseCredentials struct {
	PGDATABASE        string
	PGDATABASE_CREATE string
	PGHOST            string
	PGPASSWORD        string
	PGPASSWORD_CREATE string
	PGPORT            string
	PGSSLMODE         string
	PGUSER            string
	PGUSER_CREATE     string
	POSTGRES_SSL      string
}

type Controller struct {
	kubeclientset      kubernetes.Interface
	cloudprovclientset clientset.Interface

	jobsLister       batchlisters.JobLister
	jobsSynced       cache.InformerSynced
	postgresesLister listers.PostgresLister
	postgresesSynced cache.InformerSynced

	workqueue workqueue.RateLimitingInterface
	recorder  record.EventRecorder
	config    Config
}

func NewController(
	kubeclientset kubernetes.Interface,
	cloudprovclientset clientset.Interface,
	jobInformer batchinformers.JobInformer,
	postgresInformer informers.PostgresInformer,
	config Config) *Controller {

	utilruntime.Must(cloudprovscheme.AddToScheme(scheme.Scheme))
	DebugF("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:      kubeclientset,
		cloudprovclientset: cloudprovclientset,
		jobsLister:         jobInformer.Lister(),
		jobsSynced:         jobInformer.Informer().HasSynced,
		postgresesLister:   postgresInformer.Lister(),
		postgresesSynced:   postgresInformer.Informer().HasSynced,
		workqueue:          workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Postgreses"),
		recorder:           recorder,
		config:             config,
	}

	DebugF("Setting up event handlers")
	postgresInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueuePostgres,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueuePostgres(new)
		},
		DeleteFunc: controller.enqueuePostgres,
	})
	jobInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleJob,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*batchv1.Job)
			oldDepl := old.(*batchv1.Job)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				return
			}
			controller.handleJob(new)
		},
		DeleteFunc: controller.handleJob,
	})

	return controller
}

func (c *Controller) Run(workers int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	DebugF("Starting Postgres controller")
	DebugF("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.jobsSynced, c.postgresesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	DebugF("Starting workers")
	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	DebugF("Started workers")
	<-stopCh
	DebugF("Shutting down workers")

	return nil
}

func (c *Controller) enqueuePostgres(obj interface{}) {
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	DebugF("Enqueing new work: %v", key)
	c.workqueue.Add(key)
}

func (c *Controller) handleJob(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		DebugF("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	Infof("Processing object: %s", object.GetName())
	postgres, err := c.postgresesLister.Postgreses(object.GetLabels()["postgres-namespace"]).Get(object.GetLabels()["postgres-name"])
	if err != nil {
		DebugF("ignoring orphaned object '%s' of postgres '%s'", object.GetSelfLink(), object.GetLabels()["postgres-name"])
		return
	}
	c.enqueuePostgres(postgres)
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.syncHandler(key); err != nil {
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.workqueue.Forget(obj)
		Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	postgres, err := c.postgresesLister.Postgreses(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("postgres '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}
	if !c.isPostgresSecretPresent(postgres) {
		if c.shouldCreateCredentials(postgres) {
			DebugF("handleCredentialsCreation\n")
			return c.handleCredentialsCreation(postgres)
		}

		if c.shouldCreateDatabase(postgres) {
			DebugF("handleDatabaseCreation\n")
			return c.handleDatabaseCreation(postgres)
		}

		if c.shouldUpdatePostgresStatus(postgres) {
			DebugF("handleSecretCreation\n")
			return c.handleUpdatePostgresStatus(postgres)
		}

		if c.shouldCreateSecret(postgres) {
			DebugF("handleSecretCreation\n")
			return c.handleSecretCreation(postgres)
		}
	}

	if c.inDeletionProcess(postgres) {
		if c.shouldDeleteDatabase(postgres) {
			DebugF("handleDatabaseDeletion\n")
			return c.handleDatabaseDeletion(postgres)
		}

		if c.shouldDeleteCredentials(postgres) {
			DebugF("handleCredentialDeletion\n")
			return c.handleCredentialsDeletion(postgres)
		}

		if c.shouldDeleteFinalizers(postgres) {
			DebugF("handleFinalizersDeletion\n")
			return c.handleFinalizersDeletion(postgres)
		}
	}
	c.recorder.Event(postgres, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) isPostgresSecretPresent(postgres *v1alpha1.Postgres) bool {
	_, err := c.getPostgresSecret(postgres)
	if errors.IsNotFound(err) && postgres.ObjectMeta.DeletionTimestamp == nil {
		return false
	}
	return true
}

func (c *Controller) shouldCreateCredentials(postgres *v1alpha1.Postgres) bool {
	adminSecret, err := c.getAdminSecret(postgres)
	if errors.IsNotFound(err) {
		panicErr(fmt.Errorf("I couldn't find the admin-secret containing connexion credentials to the database"))
	}
	if adminSecret.Data[referenceName(postgres)] == nil {
		return true
	}
	return false
}

func (c *Controller) handleCredentialsCreation(postgres *v1alpha1.Postgres) error {
	adminSecret, err := c.getAdminSecret(postgres)
	if err != nil {
		return err
	}
	configurationProvider := configurationChooserFromDriver(string(adminSecret.Data["DRIVER"]))
	credentials := configurationProvider(postgres, adminSecret.Data)
	jsonCredentials, err := json.Marshal(credentials)
	adminSecret.Data[referenceName(postgres)] = jsonCredentials
	_, err = c.updateAdminSecret(adminSecret)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) shouldCreateDatabase(postgres *v1alpha1.Postgres) bool {
	adminSecret, _ := c.getAdminSecret(postgres)
	if adminSecret.Data[referenceName(postgres)] != nil {
		return true
	}
	return false
}

func (c *Controller) handleDatabaseCreation(postgres *v1alpha1.Postgres) error {
	_, err := c.jobsLister.Jobs(c.config.JobNamespace).Get(creationJobName(postgres))
	if errors.IsNotFound(err) {
		_, err = c.createDatabaseCreationJob(postgres, c.config)
	}
	return err
}

func (c *Controller) shouldUpdatePostgresStatus(postgres *v1alpha1.Postgres) bool {
	if postgres.Status.ProvisioningStatus != cloudprovv1alpha1.PostgresProvisionningSucceeded {
		return true
	}
	return false
}

func (c *Controller) handleUpdatePostgresStatus(postgres *v1alpha1.Postgres) error {
	job, err := c.jobsLister.Jobs(c.config.JobNamespace).Get(creationJobName(postgres))
	if err != nil {
		return err
	}
	_, err = c.updatePostgresStatusFromJob(postgres, job)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) shouldCreateSecret(postgres *v1alpha1.Postgres) bool {
	if postgres.Status.ProvisioningStatus == cloudprovv1alpha1.PostgresProvisionningSucceeded {
		return true
	}
	return false
}

func (c *Controller) handleSecretCreation(postgres *v1alpha1.Postgres) error {
	_, err := c.createPostgresSecret(postgres)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) inDeletionProcess(postgres *v1alpha1.Postgres) bool {
	if postgres.ObjectMeta.DeletionTimestamp != nil &&
		postgres.ObjectMeta.Finalizers[0] == "cloudprov.org/delete-database" {
		return true
	}
	return false
}

func (c *Controller) shouldDeleteDatabase(postgres *v1alpha1.Postgres) bool {
	if postgres.ObjectMeta.DeletionTimestamp != nil &&
		postgres.ObjectMeta.Finalizers[0] == "cloudprov.org/delete-database" {
		return true
	}
	return false
}

func (c *Controller) handleDatabaseDeletion(postgres *v1alpha1.Postgres) error {
	_, err := c.getDeletionJob(postgres)
	if errors.IsNotFound(err) {
		databaseCredentials, _ := c.getDatabaseCredentialsFromAdminSecret(postgres, config)
		_, err = c.createDatabaseDeletionJob(*postgres, databaseCredentials)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) shouldDeleteCredentials(postgres *v1alpha1.Postgres) bool {
	if postgres.ObjectMeta.DeletionTimestamp != nil {
		job, err := c.jobsLister.Jobs(c.config.JobNamespace).Get(deletionJobName(postgres))
		if errors.IsNotFound(err) {
			return false
		}
		if job.Status.Succeeded == 1 {
			adminSecret, _ := c.getAdminSecret(postgres)
			if _, present := adminSecret.Data[referenceName(postgres)]; present {
				return true
			}
		}
	}
	return false
}

func (c *Controller) handleCredentialsDeletion(postgres *v1alpha1.Postgres) error {
	adminSecret, err := c.getAdminSecret(postgres)
	if err != nil {
		return err
	}
	delete(adminSecret.Data, referenceName(postgres))
	_, err = c.updateAdminSecret(adminSecret)
	if err != nil {
		return err
	}
	return nil
}

func (c *Controller) shouldDeleteFinalizers(postgres *v1alpha1.Postgres) bool {
	if postgres.ObjectMeta.DeletionTimestamp == nil ||
		len(postgres.ObjectMeta.Finalizers) == 0 {
		return false
	}
	_, err := c.getPostgresSecret(postgres)
	if !errors.IsNotFound(err) {
		return false
	}
	return true
}

func (c *Controller) handleFinalizersDeletion(postgres *v1alpha1.Postgres) error {
	return c.updateDeletionFinalizer(postgres)
}
