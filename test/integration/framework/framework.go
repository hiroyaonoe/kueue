/*
Copyright 2022 The Kubernetes Authors.

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

package framework

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1alpha1"
	"sigs.k8s.io/kueue/pkg/workload"
	// +kubebuilder:scaffold:imports
)

type ManagerSetup func(manager.Manager, context.Context)

type Framework struct {
	CRDPath      string
	WebhookPath  string
	ManagerSetup ManagerSetup
	testEnv      *envtest.Environment
	cancel       context.CancelFunc
}

func (f *Framework) Setup() (context.Context, *rest.Config, client.Client) {
	ctrl.SetLogger(zap.New(zap.WriteTo(ginkgo.GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.Level(-3))))

	ginkgo.By("bootstrapping test environment")
	f.testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{f.CRDPath},
		ErrorIfCRDPathMissing: true,
	}
	webhookEnabled := len(f.WebhookPath) > 0
	if webhookEnabled {
		f.testEnv.WebhookInstallOptions.Paths = []string{f.WebhookPath}
	}

	cfg, err := f.testEnv.Start()
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
	gomega.ExpectWithOffset(1, cfg).NotTo(gomega.BeNil())

	err = kueue.AddToScheme(scheme.Scheme)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
	gomega.ExpectWithOffset(1, k8sClient).NotTo(gomega.BeNil())

	webhookInstallOptions := &f.testEnv.WebhookInstallOptions
	mgrOpts := manager.Options{
		Scheme:             scheme.Scheme,
		MetricsBindAddress: "0", // disable metrics to avoid conflicts between packages.
		Host:               webhookInstallOptions.LocalServingHost,
		Port:               webhookInstallOptions.LocalServingPort,
		CertDir:            webhookInstallOptions.LocalServingCertDir,
	}
	mgr, err := ctrl.NewManager(cfg, mgrOpts)
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred(), "failed to create manager")

	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.ManagerSetup(mgr, ctx)

	go func() {
		defer ginkgo.GinkgoRecover()
		err := mgr.Start(ctx)
		gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred(), "failed to run manager")
	}()

	if webhookEnabled {
		// wait for the webhook server to get ready
		dialer := &net.Dialer{Timeout: time.Second}
		addrPort := fmt.Sprintf("%s:%d", webhookInstallOptions.LocalServingHost, webhookInstallOptions.LocalServingPort)
		gomega.Eventually(func() error {
			conn, err := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				return err
			}
			conn.Close()
			return nil
		}).Should(gomega.Succeed())
	}

	return ctx, cfg, k8sClient
}

func (f *Framework) Teardown() {
	ginkgo.By("tearing down the test environment")
	f.cancel()
	err := f.testEnv.Stop()
	gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
}

func DeleteClusterQueue(ctx context.Context, c client.Client, cq *kueue.ClusterQueue) error {
	if cq != nil {
		if err := c.Delete(ctx, cq); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func DeleteResourceFlavor(ctx context.Context, c client.Client, rf *kueue.ResourceFlavor) error {
	if rf != nil {
		if err := c.Delete(ctx, rf); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func DeleteQueue(ctx context.Context, c client.Client, q *kueue.Queue) error {
	if q != nil {
		if err := c.Delete(ctx, q); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// DeleteNamespace deletes all objects the tests typically create in the namespace.
func DeleteNamespace(ctx context.Context, c client.Client, ns *corev1.Namespace) error {
	if ns == nil {
		return nil
	}
	err := c.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(ns.Name), client.PropagationPolicy(metav1.DeletePropagationBackground))
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.DeleteAllOf(ctx, &kueue.Queue{}, client.InNamespace(ns.Name)); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.DeleteAllOf(ctx, &kueue.Workload{}, client.InNamespace(ns.Name)); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err := c.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func ExpectWorkloadsToBeAdmitted(ctx context.Context, k8sClient client.Client, cqName string, wls ...*kueue.Workload) {
	gomega.EventuallyWithOffset(1, func() int {
		admitted := 0
		var updatedWorkload kueue.Workload
		for _, wl := range wls {
			gomega.ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(wl), &updatedWorkload)).To(gomega.Succeed())
			if updatedWorkload.Spec.Admission != nil && string(updatedWorkload.Spec.Admission.ClusterQueue) == cqName {
				admitted++
			}
		}
		return admitted
	}).Should(gomega.Equal(len(wls)), "Not enough workloads were admitted")
}

func ExpectWorkloadsToBePending(ctx context.Context, k8sClient client.Client, wls ...*kueue.Workload) {
	gomega.EventuallyWithOffset(1, func() int {
		pending := 0
		var updatedWorkload kueue.Workload
		for _, wl := range wls {
			gomega.ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(wl), &updatedWorkload)).To(gomega.Succeed())
			idx := workload.FindConditionIndex(&updatedWorkload.Status, kueue.WorkloadAdmitted)
			if idx == -1 {
				continue
			}
			cond := updatedWorkload.Status.Conditions[idx]
			if cond.Status == corev1.ConditionFalse && cond.Reason == "Pending" && wl.Spec.Admission == nil {
				pending++
			}
		}
		return pending
	}, Timeout, Interval).Should(gomega.Equal(len(wls)), "Not enough workloads are pending")
}

func UpdateWorkloadStatus(ctx context.Context, k8sClient client.Client, wl *kueue.Workload, update func(*kueue.Workload)) {
	gomega.EventuallyWithOffset(1, func() error {
		var updatedWl kueue.Workload
		gomega.ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(wl), &updatedWl)).To(gomega.Succeed())
		update(&updatedWl)
		return k8sClient.Status().Update(ctx, &updatedWl)
	}, Timeout, Interval).Should(gomega.Succeed())
}
