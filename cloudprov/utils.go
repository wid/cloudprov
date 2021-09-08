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
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

func init() {
	flag.StringVar(&config.Kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&config.MasterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&config.AdminSecret, "admin-secret", "admin-secret", "The name of the Secret containing the connection information for Database Creation.")
	flag.IntVar(&config.TTLSecondsAfterFinished, "ttl-seconds-after-finished", 3600, "Time to let the jobs around before garbage collection")
	flag.BoolVar(&config.Debug, "debug", true, "Debug mode")
	flag.StringVar(&config.NameLabel, "name-label", os.Getenv("NAME_LABEL"), "Name label of the pod")
	flag.StringVar(&config.InstanceLabel, "instance-label", os.Getenv("INSTANCE_LABEL"), "Instance label of the pod")
}

func envValueFromKey(envVars []corev1.EnvVar, searchString string) string {
	for _, envVar := range envVars {
		if envVar.Name == searchString {
			return envVar.Value
		}
	}
	return ""
}

func getPodNamespace() string {
	namespace, _ := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	return string(namespace)
}

func panicErr(err error) {
	if err != nil {
		panic(err)
	}
}

func referenceName(postgres *v1alpha1.Postgres) string {
	return fmt.Sprintf("%v-%v", postgres.Namespace, postgres.Name)
}

func creationJobName(postgres *v1alpha1.Postgres) string {
	return fmt.Sprintf("%v-%v-create", postgres.Namespace, postgres.Name)
}
func deletionJobName(postgres *v1alpha1.Postgres) string {
	return fmt.Sprintf("%v-%v-delete", postgres.Namespace, postgres.Name)
}

func DebugF(format string, a ...interface{}) {
	if config.Debug {
		klog.V(4).Infof(format, a...)
	}
}

func Infof(format string, a ...interface{}) {
	klog.V(4).Infof(format, a...)
}

func Errorf(format string, a ...interface{}) {
	klog.V(4).Infof(format, a...)
}
