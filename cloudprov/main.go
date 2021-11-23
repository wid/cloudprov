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
	"flag"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	clientset "cloudprov.org/cloudprov-controller/pkg/generated/clientset/versioned"
	informers "cloudprov.org/cloudprov-controller/pkg/generated/informers/externalversions"
	"cloudprov.org/cloudprov-controller/pkg/signals"
)

type Config struct {
	AdminSecret               string
	MasterURL                 string
	Kubeconfig                string
	JobNamespace              string
	TTLSecondsAfterFinished   int
	TTLSecondsAfterFinished32 int32
	Debug                     bool
	NameLabel                 string
	InstanceLabel             string
	SelfPod                   *corev1.Pod
}

var config Config

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(config.MasterURL, config.Kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	cloudprovClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building cloudprov clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	cloudprovInformerFactory := informers.NewSharedInformerFactory(cloudprovClient, time.Second*30)

	config.JobNamespace = getPodNamespace()

	podList, err := kubeClient.CoreV1().Pods(getPodNamespace()).List(context.TODO(), v1.ListOptions{LabelSelector: getSelfSelector().String()})
	panicErr(err)
	if len(podList.Items) > 1 {
		panicErr(fmt.Errorf("Found too many cloudprov running"))
	}
	config.SelfPod = podList.Items[0].DeepCopy()

	config.TTLSecondsAfterFinished32 = int32(config.TTLSecondsAfterFinished)
	controller := NewController(kubeClient, cloudprovClient,
		kubeInformerFactory.Batch().V1().Jobs(),
		cloudprovInformerFactory.Samplecontroller().V1alpha1().Postgreses(),
		config)

	kubeInformerFactory.Start(stopCh)
	cloudprovInformerFactory.Start(stopCh)

	if err = controller.Run(1, stopCh); err != nil {
		klog.Fatalf("Error running controller: %s", err.Error())
	}
}

func getSelfSelector() labels.Selector {

	teamReq, err := labels.NewRequirement("app.kubernetes.io/name", selection.Equals, []string{os.Getenv("NAME_LABEL")})
	panicErr(err)
	serviceReq, err := labels.NewRequirement("app.kubernetes.io/instance", selection.Equals, []string{os.Getenv("INSTANCE_LABEL")})
	panicErr(err)

	selector := labels.NewSelector()
	selector = selector.Add(*teamReq, *serviceReq)
	return selector
}
