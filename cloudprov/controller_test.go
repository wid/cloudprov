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
	"reflect"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	kubeinformers "k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	cloudprovcontroller "cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
	cloudprovv1alpha1 "cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
	"cloudprov.org/cloudprov-controller/pkg/generated/clientset/versioned/fake"
	informers "cloudprov.org/cloudprov-controller/pkg/generated/informers/externalversions"
)

var (
	alwaysReady        = func() bool { return true }
	noResyncPeriodFunc = func() time.Duration { return 0 }
)

type fixture struct {
	t *testing.T

	client     *fake.Clientset
	kubeclient *k8sfake.Clientset
	// Objects to put in the store.
	postgresLister []*cloudprovcontroller.Postgres
	jobLister      []*batchv1.Job
	// Actions expected to happen on the client.
	kubeactions []core.Action
	actions     []core.Action
	// Objects from here preloaded into NewSimpleFake.
	kubeobjects []runtime.Object
	objects     []runtime.Object
}

func newAdminSecret(namespace string, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"DRIVER":     []byte("test"),
			"PGHOST":     []byte("host"),
			"PGPASSWORD": []byte("password"),
			"PGPORT":     []byte("5432"),
			"PGSSLMODE":  []byte("allow"),
			"PGUSER":     []byte("user"),
		},
	}
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{}
	f.t = t
	f.objects = []runtime.Object{}
	f.kubeobjects = []runtime.Object{}
	return f
}

func newPostgres(namespace string, name string) *cloudprovcontroller.Postgres {
	return &cloudprovcontroller.Postgres{
		TypeMeta: metav1.TypeMeta{APIVersion: cloudprovcontroller.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: cloudprovcontroller.PostgresSpec{
			DatabaseName: fmt.Sprintf("%s-databaseName", name),
		},
	}
}

func (f *fixture) newController() (*Controller, informers.SharedInformerFactory, kubeinformers.SharedInformerFactory) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}

	f.client = fake.NewSimpleClientset(f.objects...)
	f.kubeclient = k8sfake.NewSimpleClientset(f.kubeobjects...)

	i := informers.NewSharedInformerFactory(f.client, noResyncPeriodFunc())
	k8sI := kubeinformers.NewSharedInformerFactory(f.kubeclient, noResyncPeriodFunc())

	c := NewController(
		f.kubeclient,
		f.client,
		k8sI.Batch().V1().Jobs(),
		i.Samplecontroller().V1alpha1().Postgreses(),
		config,
	)

	c.postgresesSynced = alwaysReady
	c.jobsSynced = alwaysReady
	c.recorder = &record.FakeRecorder{}

	for _, f := range f.postgresLister {
		i.Samplecontroller().V1alpha1().Postgreses().Informer().GetIndexer().Add(f)
	}

	for _, d := range f.jobLister {
		k8sI.Batch().V1().Jobs().Informer().GetIndexer().Add(d)
	}

	return c, i, k8sI
}

func (f *fixture) run(postgresName string) {
	f.runController(postgresName, true, false)
}

func (f *fixture) runExpectError(postgresName string) {
	f.runController(postgresName, true, true)
}

func (f *fixture) runController(postgresName string, startInformers bool, expectError bool) {
	c, i, k8sI := f.newController()
	if startInformers {
		stopCh := make(chan struct{})
		defer close(stopCh)
		i.Start(stopCh)
		k8sI.Start(stopCh)
	}

	err := c.syncHandler(postgresName)
	if !expectError && err != nil {
		f.t.Errorf("error syncing postgres: %v", err)
	} else if expectError && err == nil {
		f.t.Error("expected error syncing postgres, got nil")
	}

	actions := filterInformerActions(f.client.Actions())
	for i, action := range actions {
		if len(f.actions) < i+1 {
			for indexAction, displayAction := range actions[i:] {
				fmt.Printf("Unexpected cloudprov action %v: %v\n", indexAction, displayAction)
			}

			f.t.Errorf("%d unexpected cloudprov actions\n", len(actions)-len(f.actions))
			break
		}

		expectedAction := f.actions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.actions) > len(actions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.actions)-len(actions), f.actions[len(actions):])
	}

	k8sActions := filterInformerActions(f.kubeclient.Actions())
	for i, action := range k8sActions {
		if len(f.kubeactions) < i+1 {
			for indexAction, displayAction := range k8sActions[i:] {
				fmt.Printf("Unexpected k8s action %v: %v\n", indexAction, displayAction)
			}
			f.t.Errorf("%d unexpected k8s actions", len(k8sActions)-len(f.kubeactions))
			break
		}

		expectedAction := f.kubeactions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.kubeactions) > len(k8sActions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.kubeactions)-len(k8sActions), f.kubeactions[len(k8sActions):])
	}
}

// checkAction verifies that expected and actual actions are equal and both have
// same attached resources
func checkAction(expected, actual core.Action, t *testing.T) {
	if !(expected.Matches(actual.GetVerb(), actual.GetResource().Resource) && actual.GetSubresource() == expected.GetSubresource()) {
		t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expected, actual)
		return
	}

	if reflect.TypeOf(actual) != reflect.TypeOf(expected) {
		t.Errorf("Action has wrong type. Expected: %t. Got: %t", expected, actual)
		return
	}

	switch a := actual.(type) {
	case core.GetActionImpl:
		e, _ := expected.(core.GetActionImpl)
		expObject := e
		object := a

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case core.CreateActionImpl:
		e, _ := expected.(core.CreateActionImpl)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case core.UpdateActionImpl:
		e, _ := expected.(core.UpdateActionImpl)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case core.PatchActionImpl:
		e, _ := expected.(core.PatchActionImpl)
		expPatch := e.GetPatch()
		patch := a.GetPatch()

		if !reflect.DeepEqual(expPatch, patch) {
			t.Errorf("Action %s %s has wrong patch\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expPatch, patch))
		}
	default:
		t.Errorf("Uncaptured Action %s %s, you should explicitly add a case to capture it",
			actual.GetVerb(), actual.GetResource().Resource)
	}
}

// filterInformerActions filters list and watch actions for testing resources.
// Since list and watch don't change resource state we can filter it to lower
// nose level in our tests.
func filterInformerActions(actions []core.Action) []core.Action {
	ret := []core.Action{}
	for _, action := range actions {
		if len(action.GetNamespace()) == 0 &&
			(action.Matches("list", "postgreses") ||
				action.Matches("watch", "postgreses") ||
				action.Matches("list", "jobs") ||
				action.Matches("watch", "jobs")) {
			continue
		}
		ret = append(ret, action)
	}

	return ret
}

func (f *fixture) expectGetSecretAction(namespace string, name string) {
	f.kubeactions = append(f.kubeactions, core.NewGetAction(schema.GroupVersionResource{Resource: "secrets", Version: "v1"}, namespace, name))
}

func (f *fixture) expectUpdateAdminSecret(adminSecret *corev1.Secret) {
	f.kubeactions = append(f.kubeactions, core.NewUpdateAction(schema.GroupVersionResource{Resource: "secrets", Version: "v1"}, adminSecret.Namespace, adminSecret))
}

func (f *fixture) expectCreatePostgresUpdateAction(postgres *cloudprovcontroller.Postgres) {
	f.actions = append(f.actions, core.NewUpdateAction(schema.GroupVersionResource{Resource: "postgreses", Version: "v1alpha1", Group: "cloudprov.org"}, "not-kube-system", postgres))
}

func (f *fixture) expectCreateSecretAction(secret *corev1.Secret) {
	f.kubeactions = append(f.kubeactions, core.NewCreateAction(schema.GroupVersionResource{Resource: "secrets", Version: "v1"}, secret.Namespace, secret))
}

func (f *fixture) expectCreateJobAction(job *batchv1.Job) {
	f.kubeactions = append(f.kubeactions, core.NewCreateAction(schema.GroupVersionResource{Resource: "jobs"}, job.Namespace, job))
}

func (f *fixture) expectUpdateJobAction(job *batchv1.Job) {
	f.kubeactions = append(f.kubeactions, core.NewUpdateAction(schema.GroupVersionResource{Resource: "jobs"}, job.Namespace, job))
}

func (f *fixture) expectUpdatePostgresStatusAction(postgres *cloudprovcontroller.Postgres) {
	action := core.NewUpdateAction(schema.GroupVersionResource{Resource: "postgreses"}, postgres.Namespace, postgres)
	// TODO: Until #38113 is merged, we can't use Subresource
	//action.Subresource = "status"
	f.actions = append(f.actions, action)
}

func getKey(postgres *cloudprovcontroller.Postgres, t *testing.T) string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(postgres)
	if err != nil {
		t.Errorf("Unexpected error getting key for postgres %v: %v", postgres.Name, err)
		return ""
	}
	return key
}

func updatedAdminSecret(postgres *cloudprovcontroller.Postgres, adminSecret *corev1.Secret) *corev1.Secret {
	postgresCredentials := testConfigurationGenerator(postgres, adminSecret.Data)
	jsonPostgresCredentials, _ := json.Marshal(postgresCredentials)
	var updatedAdminsecret corev1.Secret
	adminSecret.DeepCopyInto(&updatedAdminsecret)
	updatedAdminsecret.Data[referenceName(postgres)] = jsonPostgresCredentials
	return &updatedAdminsecret
}

func postgresSecret(postgres *cloudprovcontroller.Postgres, adminSecret *corev1.Secret) *corev1.Secret {
	postgresCredentials := testConfigurationGenerator(postgres, adminSecret.Data)
	jsonPostgresCredentials, _ := json.Marshal(postgresCredentials)
	var updatedAdminsecret corev1.Secret
	adminSecret.DeepCopyInto(&updatedAdminsecret)
	updatedAdminsecret.Data[referenceName(postgres)] = jsonPostgresCredentials
	return &updatedAdminsecret
}

func TestDeleteJob(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}
	currentTime := metav1.NewTime(time.Now())
	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	postgres.Finalizers = []string{"cloudprov.org/delete-database"}

	postgres.ObjectMeta.DeletionTimestamp = &currentTime
	adminSecret := updatedAdminSecret(postgres, newAdminSecret("kube-system", "admin-secret"))

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)
	authSecret, _ := newAuthSecret(postgres, adminSecret)
	f.kubeobjects = append(f.kubeobjects, authSecret)

	masterConfiguration := make(map[string][]byte)
	databaseCredentials := testConfigurationGenerator(postgres, masterConfiguration)
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectCreateJobAction(newDatabaseDeletionJob(postgres, &databaseCredentials, config))

	f.runExpectError(getKey(postgres, t))
}

func TestDeleteJobNoEntry(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}
	currentTime := metav1.NewTime(time.Now())
	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	postgres.Finalizers = []string{"cloudprov.org/delete-database"}

	postgres.ObjectMeta.DeletionTimestamp = &currentTime
	postgres.Status.UserName = "deleteUsername"
	postgres.Status.DatabaseName = "deleteDatabaseName"
	postgres.ObjectMeta.DeletionTimestamp = &currentTime

	adminSecret := newAdminSecret("kube-system", "admin-secret")
	authSecret, _ := newAuthSecret(postgres, updatedAdminSecret(postgres, newAdminSecret("kube-system", "admin-secret")))

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)
	f.kubeobjects = append(f.kubeobjects, authSecret)

	postgres.Finalizers = nil
	f.expectCreatePostgresUpdateAction(postgres)
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")

	f.run(getKey(postgres, t))
}

func TestFinalizerDeletion(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}

	currentTime := metav1.NewTime(time.Now())
	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	postgres.Finalizers = []string{"cloudprov.org/delete-database"}

	postgres.ObjectMeta.DeletionTimestamp = &currentTime
	postgres.Status.UserName = "deleteUsername"
	postgres.Status.DatabaseName = "deleteDatabaseName"
	postgres.ObjectMeta.DeletionTimestamp = &currentTime

	adminSecret := updatedAdminSecret(postgres, newAdminSecret("kube-system", "admin-secret"))

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)
	authSecret, _ := newAuthSecret(postgres, adminSecret)
	f.kubeobjects = append(f.kubeobjects, authSecret)
	deletionJob := newDatabaseDeletionJob(postgres, &DatabaseCredentials{}, config)
	deletionJob.Status.Succeeded = 1
	f.kubeobjects = append(f.kubeobjects, deletionJob)
	f.jobLister = append(f.jobLister, deletionJob)

	postgres.Finalizers = nil
	f.expectCreatePostgresUpdateAction(postgres)
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")

	f.run(getKey(postgres, t))
}

func TestCreatesCredentials(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}

	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	adminSecret := newAdminSecret("kube-system", "admin-secret")

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)

	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("kube-system", "admin-secret")

	updatedAdminSecret(postgres, adminSecret)
	f.expectUpdateAdminSecret(updatedAdminSecret(postgres, adminSecret))

	f.runExpectError(getKey(postgres, t))

}

func TestCreatesJob(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}

	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	adminSecret := updatedAdminSecret(postgres, newAdminSecret("kube-system", "admin-secret"))

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)

	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("kube-system", "admin-secret")

	f.expectCreateJobAction(newDatabaseCreationJob(postgres, testConfigurationGenerator(postgres, adminSecret.Data), config))
	f.runExpectError(getKey(postgres, t))

}

func TestCreatesSecret(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}

	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	adminSecret := updatedAdminSecret(postgres, newAdminSecret("kube-system", "admin-secret"))

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)
	completedDatabaseCreationJob := newDatabaseCreationJob(postgres, testConfigurationGenerator(postgres, adminSecret.Data), config)
	completedDatabaseCreationJob.Status.Succeeded = 1
	f.kubeobjects = append(f.kubeobjects, completedDatabaseCreationJob)
	f.jobLister = append(f.jobLister, completedDatabaseCreationJob)

	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("kube-system", "admin-secret")

	var updatedPostgres cloudprovcontroller.Postgres
	postgres.DeepCopyInto(&updatedPostgres)
	updatedPostgres.Status.ProvisioningStatus = cloudprovv1alpha1.PostgresProvisionningSucceeded
	f.expectCreatePostgresUpdateAction(&updatedPostgres)

	authSecret, err := newAuthSecret(postgres, adminSecret)
	panicErr(err)
	f.expectCreateSecretAction(authSecret)

	f.run(getKey(postgres, t))
}

func TestDoNothing(t *testing.T) {
	config = Config{JobNamespace: "kube-system", AdminSecret: "admin-secret", SelfPod: testSelfPod()}

	f := newFixture(t)
	postgres := newPostgres("not-kube-system", "test")
	adminSecret := updatedAdminSecret(postgres, newAdminSecret("kube-system", "admin-secret"))

	f.postgresLister = append(f.postgresLister, postgres)
	f.objects = append(f.objects, postgres)
	f.kubeobjects = append(f.kubeobjects, adminSecret)
	authSecret, _ := newAuthSecret(postgres, adminSecret)
	f.kubeobjects = append(f.kubeobjects, authSecret)

	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")
	f.expectGetSecretAction("not-kube-system", "test")
	f.expectGetSecretAction("kube-system", "admin-secret")

	f.run(getKey(postgres, t))
}

func testSelfPod() *corev1.Pod {

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloudprov-controller",
			Namespace: config.JobNamespace,
			UID:       "9e495733-dc6e-4a04-aa5e-9fe696d2e357",
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{},
		},
	}
}
