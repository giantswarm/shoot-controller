/*
Copyright 2025.

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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster Controller", func() {
	var (
		ctx                 context.Context
		cancel              context.CancelFunc
		reconciler          *ClusterReconciler
		testShootVersion    = "1.0.0"
		testCatalogName     = "test-catalog"
		testClusterID       = "test-cluster"
		testOrgNamespace    = "org-test"
		testHelmReleaseName = testClusterID + "-shoot"
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		reconciler = &ClusterReconciler{
			Client:       k8sClient,
			Scheme:       scheme.Scheme,
			ShootVersion: testShootVersion,
			CatalogName:  testCatalogName,
		}
	})

	AfterEach(func() {
		cancel()
	})

	Context("When reconciling a Cluster resource", func() {
		BeforeEach(func() {
			// Create test namespace - wait for deletion if terminating
			ns := &corev1.Namespace{}
			ns.Name = testOrgNamespace
			err := k8sClient.Get(ctx, client.ObjectKey{Name: testOrgNamespace}, ns)
			if err != nil {
				Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			} else if ns.DeletionTimestamp != nil {
				// Namespace is terminating, wait for it to be deleted
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: testOrgNamespace}, ns)
					return err != nil
				}).Should(BeTrue())
				// Create fresh namespace
				ns = &corev1.Namespace{}
				ns.Name = testOrgNamespace
				Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			}

			// Wait for any previous cluster to be fully deleted (ignore if not found)
			cluster := &unstructured.Unstructured{}
			cluster.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   clusterAPIGroup,
				Version: clusterAPIVersion,
				Kind:    clusterKind,
			})
			cluster.SetName(testClusterID)
			cluster.SetNamespace(testOrgNamespace)
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: testClusterID, Namespace: testOrgNamespace}, cluster)
				return err != nil // Return true when cluster is not found (deleted)
			}, "10s").Should(BeTrue())
		})

		AfterEach(func() {
			// Clean up test resources
			cluster := &unstructured.Unstructured{}
			cluster.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   clusterAPIGroup,
				Version: clusterAPIVersion,
				Kind:    clusterKind,
			})
			cluster.SetName(testClusterID)
			cluster.SetNamespace(testOrgNamespace)
			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))

			helmRelease := &unstructured.Unstructured{}
			helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   helmReleaseAPIGroup,
				Version: helmReleaseAPIVersion,
				Kind:    helmReleaseKind,
			})
			helmRelease.SetName(testHelmReleaseName)
			helmRelease.SetNamespace(testOrgNamespace)
			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, helmRelease))
		})

		It("should successfully create a HelmRelease when Cluster is created", func() {
			// Create Cluster
			cluster := createTestCluster(testClusterID, testOrgNamespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testClusterID,
					Namespace: testOrgNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify HelmRelease was created
			helmRelease := &unstructured.Unstructured{}
			helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   helmReleaseAPIGroup,
				Version: helmReleaseAPIVersion,
				Kind:    helmReleaseKind,
			})
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testHelmReleaseName,
				Namespace: testOrgNamespace,
			}, helmRelease)).To(Succeed())

			// Verify HelmRelease spec - use NestedFieldNoCopy to avoid deep copy issues
			specObj, found, err := unstructured.NestedFieldNoCopy(helmRelease.Object, "spec")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			spec := specObj.(map[string]interface{})

			// Verify chart spec
			chartSpecObj, found, err := unstructured.NestedFieldNoCopy(spec, "chart", "spec")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			chartSpec := chartSpecObj.(map[string]interface{})
			Expect(chartSpec["chart"]).To(Equal("shoot"))
			Expect(chartSpec["version"]).To(Equal(testShootVersion))
			Expect(chartSpec["reconcileStrategy"]).To(Equal("ChartVersion"))

			// Verify sourceRef
			sourceRefObj, found, err := unstructured.NestedFieldNoCopy(chartSpec, "sourceRef")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			sourceRef := sourceRefObj.(map[string]interface{})
			Expect(sourceRef["kind"]).To(Equal("HelmRepository"))
			Expect(sourceRef["name"]).To(Equal(testClusterID + "-" + testCatalogName))

			// Verify labels
			labels := helmRelease.GetLabels()
			Expect(labels["app.kubernetes.io/name"]).To(Equal("shoot"))
			Expect(labels["app.kubernetes.io/instance"]).To(Equal("shoot-" + testClusterID))
			Expect(labels["app.kubernetes.io/version"]).To(Equal(testShootVersion))
			Expect(labels["app.kubernetes.io/managed-by"]).To(Equal("shoot-controller"))
			Expect(labels["application.giantswarm.io/team"]).To(Equal("phoenix"))
			Expect(labels["giantswarm.io/cluster"]).To(Equal(testClusterID))

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, helmRelease)).To(Succeed())
		})

		It("should use Cluster namespace for HelmRelease namespace", func() {
			customNamespace := "org-custom"
			// Create custom namespace
			ns := &corev1.Namespace{}
			ns.Name = customNamespace
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			defer func() {
				_ = client.IgnoreNotFound(k8sClient.Delete(ctx, ns))
			}()

			cluster := createTestCluster(testClusterID, customNamespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testClusterID,
					Namespace: customNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify HelmRelease is in the same namespace as Cluster
			helmRelease := &unstructured.Unstructured{}
			helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   helmReleaseAPIGroup,
				Version: helmReleaseAPIVersion,
				Kind:    helmReleaseKind,
			})
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testHelmReleaseName,
				Namespace: customNamespace,
			}, helmRelease)).To(Succeed())

			Expect(helmRelease.GetNamespace()).To(Equal(customNamespace))

			// Verify storageNamespace and targetNamespace use cluster namespace
			specObj, _, _ := unstructured.NestedFieldNoCopy(helmRelease.Object, "spec")
			spec := specObj.(map[string]interface{})
			storageNamespace, _, _ := unstructured.NestedString(spec, "storageNamespace")
			targetNamespace, _, _ := unstructured.NestedString(spec, "targetNamespace")
			Expect(storageNamespace).To(Equal(customNamespace))
			Expect(targetNamespace).To(Equal(customNamespace))

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, helmRelease)).To(Succeed())
		})

		It("should update HelmRelease when Cluster is updated", func() {
			// Create Cluster
			cluster := createTestCluster(testClusterID, testOrgNamespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// First reconcile - will add finalizer and create HelmRelease
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testClusterID,
					Namespace: testOrgNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Update Cluster (add a label)
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testClusterID,
				Namespace: testOrgNamespace,
			}, cluster)).To(Succeed())
			cluster.SetLabels(map[string]string{
				"test-label": "test-value",
			})
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

			// Reconcile again
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify HelmRelease still exists
			helmRelease := &unstructured.Unstructured{}
			helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   helmReleaseAPIGroup,
				Version: helmReleaseAPIVersion,
				Kind:    helmReleaseKind,
			})
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testHelmReleaseName,
				Namespace: testOrgNamespace,
			}, helmRelease)).To(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, helmRelease)).To(Succeed())
		})

		It("should set owner reference on HelmRelease for automatic deletion", func() {
			// Create Cluster
			cluster := createTestCluster(testClusterID, testOrgNamespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Reconcile to create HelmRelease
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testClusterID,
					Namespace: testOrgNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify HelmRelease exists and has owner reference
			helmRelease := &unstructured.Unstructured{}
			helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   helmReleaseAPIGroup,
				Version: helmReleaseAPIVersion,
				Kind:    helmReleaseKind,
			})
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testHelmReleaseName,
				Namespace: testOrgNamespace,
			}, helmRelease)).To(Succeed())

			// Verify owner reference is set correctly
			ownerRefs, found, err := unstructured.NestedSlice(helmRelease.Object, "metadata", "ownerReferences")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(len(ownerRefs)).To(Equal(1))

			ownerRef := ownerRefs[0].(map[string]interface{})
			Expect(ownerRef["kind"]).To(Equal(clusterKind))
			Expect(ownerRef["name"]).To(Equal(testClusterID))
			Expect(ownerRef["controller"]).To(Equal(true))

			// Note: In a real Kubernetes cluster, deleting the Cluster would automatically
			// delete the HelmRelease via the owner reference. In envtest, garbage collection
			// may not be enabled, so we just verify the owner reference is set correctly.

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, helmRelease)).To(Succeed())
		})

		It("should handle Cluster not found gracefully", func() {
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent-cluster",
					Namespace: testOrgNamespace,
				},
			}
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
		})

		It("should set owner reference on HelmRelease", func() {
			// Create Cluster
			cluster := createTestCluster(testClusterID, testOrgNamespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testClusterID,
					Namespace: testOrgNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify owner reference
			helmRelease := &unstructured.Unstructured{}
			helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   helmReleaseAPIGroup,
				Version: helmReleaseAPIVersion,
				Kind:    helmReleaseKind,
			})
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testHelmReleaseName,
				Namespace: testOrgNamespace,
			}, helmRelease)).To(Succeed())

			ownerRefs, found, err := unstructured.NestedSlice(helmRelease.Object, "metadata", "ownerReferences")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(len(ownerRefs)).To(Equal(1))

			ownerRef := ownerRefs[0].(map[string]interface{})
			Expect(ownerRef["kind"]).To(Equal(clusterKind))
			Expect(ownerRef["name"]).To(Equal(testClusterID))
			Expect(ownerRef["controller"]).To(Equal(true))

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, helmRelease)).To(Succeed())
		})
	})

	Context("Helper functions", func() {
		It("should build HelmRelease with correct structure", func() {
			cluster := createTestCluster(testClusterID, testOrgNamespace)

			// Need to create cluster in k8s first so it has a UID for owner reference
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer func() {
				_ = client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))
			}()

			// Refetch cluster to get server-assigned UID
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testClusterID,
				Namespace: testOrgNamespace,
			}, cluster)).To(Succeed())

			helmRelease, err := reconciler.buildHelmRelease(cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(helmRelease.GetName()).To(Equal(testHelmReleaseName))
			Expect(helmRelease.GetNamespace()).To(Equal(testOrgNamespace))

			// Verify spec structure - access directly to avoid deep copy issues with integers
			specObj, found, err := unstructured.NestedFieldNoCopy(helmRelease.Object, "spec")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			spec := specObj.(map[string]interface{})

			// Verify all required fields
			releaseName, _, _ := unstructured.NestedString(spec, "releaseName")
			Expect(releaseName).To(Equal(testClusterID + "-shoot"))

			storageNamespace, _, _ := unstructured.NestedString(spec, "storageNamespace")
			Expect(storageNamespace).To(Equal(testOrgNamespace))

			targetNamespace, _, _ := unstructured.NestedString(spec, "targetNamespace")
			Expect(targetNamespace).To(Equal(testOrgNamespace))

			interval, _, _ := unstructured.NestedString(spec, "interval")
			Expect(interval).To(Equal("5m"))

			timeout, _, _ := unstructured.NestedString(spec, "timeout")
			Expect(timeout).To(Equal("15m"))

			// Verify kubeConfig - access directly
			kubeConfigObj, _, _ := unstructured.NestedFieldNoCopy(spec, "kubeConfig")
			Expect(kubeConfigObj).NotTo(BeNil())
			kubeConfig := kubeConfigObj.(map[string]interface{})
			secretRefObj, _, _ := unstructured.NestedFieldNoCopy(kubeConfig, "secretRef")
			secretRef := secretRefObj.(map[string]interface{})
			Expect(secretRef["name"]).To(Equal(testClusterID + "-kubeconfig"))

			// Verify valuesFrom
			valuesFrom, _, _ := unstructured.NestedSlice(spec, "valuesFrom")
			Expect(len(valuesFrom)).To(Equal(1))
			valuesFromItem := valuesFrom[0].(map[string]interface{})
			Expect(valuesFromItem["kind"]).To(Equal("ConfigMap"))
			Expect(valuesFromItem["name"]).To(Equal(testClusterID + "-cluster-values"))
			Expect(valuesFromItem["valuesKey"]).To(Equal("values"))
		})

		It("should generate correct HelmRelease name", func() {
			Expect(reconciler.helmReleaseName("test-cluster")).To(Equal("test-cluster-shoot"))
			Expect(reconciler.helmReleaseName("cluster-123")).To(Equal("cluster-123-shoot"))
		})

		It("should fail to build HelmRelease if Cluster has no UID", func() {
			// Create cluster without persisting to k8s (so it has no UID)
			cluster := createTestCluster(testClusterID, testOrgNamespace)
			cluster.SetUID("") // Clear UID to simulate missing UID

			helmRelease, err := reconciler.buildHelmRelease(cluster)
			Expect(err).To(HaveOccurred())
			Expect(helmRelease).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("failed to set owner reference"))
		})
	})
})

// createTestCluster creates a test Cluster unstructured object
func createTestCluster(clusterID, namespace string) *unstructured.Unstructured {
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   clusterAPIGroup,
		Version: clusterAPIVersion,
		Kind:    clusterKind,
	})
	cluster.SetName(clusterID)
	cluster.SetNamespace(namespace)
	cluster.SetUID(types.UID("test-uid-" + clusterID))
	return cluster
}
