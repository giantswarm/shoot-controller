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
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	clusterAPIGroup       = "cluster.x-k8s.io"
	clusterAPIVersion     = "v1beta1"
	clusterKind           = "Cluster"
	helmReleaseAPIGroup   = "helm.toolkit.fluxcd.io"
	helmReleaseAPIVersion = "v2"
	helmReleaseKind       = "HelmRelease"
)

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ShootVersion string
	CatalogName  string
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases/status,verbs=get;update;patch

// Reconcile will create the HelmRelease CR to deploy shoot
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	cluster, err := r.getCluster(ctx, req.Name, req.Namespace)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling Cluster", "name", cluster.GetName(), "namespace", cluster.GetNamespace())

	// Skip reconciliation if Cluster is being deleted.
	// The HelmRelease will be automatically deleted by Kubernetes' garbage collector
	// via the owner reference (with controller: true) set in buildHelmRelease.
	if cluster.GetDeletionTimestamp() != nil {
		log.Info("Cluster is being deleted, skipping reconciliation", "name", cluster.GetName())
		return ctrl.Result{}, nil
	}

	return r.reconcileNormal(ctx, cluster, log)
}

// reconcileNormal handles the creation or update of a Cluster resource
func (r *ClusterReconciler) reconcileNormal(ctx context.Context, cluster *unstructured.Unstructured, log logr.Logger) (ctrl.Result, error) {
	// Build HelmRelease with owner reference
	helmRelease, err := r.buildHelmRelease(cluster)
	if err != nil {
		log.Error(err, "Failed to build HelmRelease",
			"cluster", cluster.GetName(),
			"namespace", cluster.GetNamespace())
		return ctrl.Result{}, fmt.Errorf("failed to build HelmRelease for Cluster %s: %w",
			cluster.GetName(), err)
	}

	// Check if HelmRelease already exists
	existingHelmRelease, err := r.getHelmRelease(ctx, helmRelease.GetName(), helmRelease.GetNamespace())
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to fetch HelmRelease",
			"helmRelease", helmRelease.GetName(),
			"namespace", helmRelease.GetNamespace(),
			"cluster", cluster.GetName())
		return ctrl.Result{}, fmt.Errorf("failed to get HelmRelease %s/%s for Cluster %s: %w",
			helmRelease.GetNamespace(), helmRelease.GetName(), cluster.GetName(), err)
	}

	if errors.IsNotFound(err) {
		// Create HelmRelease
		if err := r.Client.Create(ctx, helmRelease); err != nil {
			log.Error(err, "Failed to create HelmRelease",
				"helmRelease", helmRelease.GetName(),
				"namespace", helmRelease.GetNamespace(),
				"cluster", cluster.GetName())
			return ctrl.Result{}, fmt.Errorf("failed to create HelmRelease %s/%s for Cluster %s: %w",
				helmRelease.GetNamespace(), helmRelease.GetName(), cluster.GetName(), err)
		}
		log.Info("Created HelmRelease",
			"helmRelease", helmRelease.GetName(),
			"namespace", helmRelease.GetNamespace(),
			"cluster", cluster.GetName())
	} else {
		// Update HelmRelease spec to ensure consistency
		existingHelmRelease.Object["spec"] = helmRelease.Object["spec"]
		// Preserve existing labels but merge with new ones
		existingLabels := existingHelmRelease.GetLabels()
		newLabels := helmRelease.GetLabels()
		if existingLabels == nil {
			existingLabels = make(map[string]string)
		}
		for k, v := range newLabels {
			existingLabels[k] = v
		}
		existingHelmRelease.SetLabels(existingLabels)

		if err := r.Client.Update(ctx, existingHelmRelease); err != nil {
			log.Error(err, "Failed to update HelmRelease",
				"helmRelease", helmRelease.GetName(),
				"namespace", helmRelease.GetNamespace(),
				"cluster", cluster.GetName())
			return ctrl.Result{}, fmt.Errorf("failed to update HelmRelease %s/%s for Cluster %s: %w",
				helmRelease.GetNamespace(), helmRelease.GetName(), cluster.GetName(), err)
		}
		log.Info("Updated HelmRelease",
			"helmRelease", helmRelease.GetName(),
			"namespace", helmRelease.GetNamespace(),
			"cluster", cluster.GetName())
	}

	return ctrl.Result{}, nil
}

// getCluster fetches a Cluster resource
func (r *ClusterReconciler) getCluster(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   clusterAPIGroup,
		Version: clusterAPIVersion,
		Kind:    clusterKind,
	})
	cluster.SetName(name)
	cluster.SetNamespace(namespace)

	err := r.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, cluster)
	return cluster, err
}

// getHelmRelease fetches a HelmRelease resource
func (r *ClusterReconciler) getHelmRelease(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	helmRelease := &unstructured.Unstructured{}
	helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   helmReleaseAPIGroup,
		Version: helmReleaseAPIVersion,
		Kind:    helmReleaseKind,
	})
	helmRelease.SetName(name)
	helmRelease.SetNamespace(namespace)

	err := r.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, helmRelease)
	return helmRelease, err
}

// helmReleaseName returns the name for a HelmRelease based on cluster ID
func (r *ClusterReconciler) helmReleaseName(clusterID string) string {
	return clusterID + "-shoot"
}

// buildHelmRelease builds a HelmRelease object from a Cluster
func (r *ClusterReconciler) buildHelmRelease(cluster *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	clusterID := cluster.GetName()
	orgNamespace := cluster.GetNamespace()

	helmRelease := &unstructured.Unstructured{}
	helmRelease.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   helmReleaseAPIGroup,
		Version: helmReleaseAPIVersion,
		Kind:    helmReleaseKind,
	})
	helmRelease.SetName(r.helmReleaseName(clusterID))
	helmRelease.SetNamespace(orgNamespace)

	// Set labels
	labels := map[string]string{
		"app.kubernetes.io/name":         "shoot",
		"app.kubernetes.io/instance":     "shoot-" + clusterID,
		"app.kubernetes.io/version":      r.ShootVersion,
		"app.kubernetes.io/managed-by":   "shoot-controller",
		"application.giantswarm.io/team": "phoenix",
		"giantswarm.io/cluster":          clusterID,
	}
	helmRelease.SetLabels(labels)

	// Build spec
	spec := map[string]interface{}{
		"chart": map[string]interface{}{
			"spec": map[string]interface{}{
				"chart":             "shoot",
				"reconcileStrategy": "ChartVersion",
				"sourceRef": map[string]interface{}{
					"kind": "HelmRepository",
					"name": clusterID + "-" + r.CatalogName,
				},
				"version": r.ShootVersion,
			},
		},
		"install": map[string]interface{}{
			"createNamespace": true,
			"remediation": map[string]interface{}{
				"retries": -1,
			},
		},
		"interval": "5m",
		"kubeConfig": map[string]interface{}{
			"secretRef": map[string]interface{}{
				"name": clusterID + "-kubeconfig",
			},
		},
		"releaseName":      clusterID + "-shoot",
		"storageNamespace": orgNamespace,
		"targetNamespace":  orgNamespace,
		"timeout":          "15m",
		"upgrade": map[string]interface{}{
			"remediation": map[string]interface{}{
				"retries": -1,
			},
		},
		"valuesFrom": []interface{}{
			map[string]interface{}{
				"kind":      "ConfigMap",
				"name":      clusterID + "-cluster-values",
				"valuesKey": "values",
			},
		},
	}

	helmRelease.Object["spec"] = spec

	// Set owner reference for automatic garbage collection
	// This is critical - if we can't set the owner reference, the HelmRelease
	// won't be automatically deleted when the Cluster is deleted.
	if err := r.setOwnerReference(helmRelease, cluster); err != nil {
		return nil, fmt.Errorf("failed to set owner reference on HelmRelease: %w", err)
	}

	return helmRelease, nil
}

// setOwnerReference sets the owner reference on a HelmRelease pointing to the Cluster
func (r *ClusterReconciler) setOwnerReference(helmRelease, cluster *unstructured.Unstructured) error {
	// Get Cluster UID and API version
	clusterUID, found, err := unstructured.NestedString(cluster.Object, "metadata", "uid")
	if err != nil {
		return fmt.Errorf("error accessing cluster UID: %w", err)
	}
	if !found || clusterUID == "" {
		return fmt.Errorf("cluster has no UID (may not be persisted yet)")
	}

	clusterAPIVersionStr := clusterAPIGroup + "/" + clusterAPIVersion

	// Create owner reference
	ownerRef := metav1.OwnerReference{
		APIVersion: clusterAPIVersionStr,
		Kind:       clusterKind,
		Name:       cluster.GetName(),
		UID:        types.UID(clusterUID),
		Controller: func() *bool { b := true; return &b }(),
	}

	// Use controllerutil.SetControllerReference if possible
	// Since we're using unstructured, we'll set it manually
	ownerRefs, found, _ := unstructured.NestedSlice(helmRelease.Object, "metadata", "ownerReferences")
	if !found {
		ownerRefs = []interface{}{}
	}

	// Convert ownerRef to map[string]interface{}
	ownerRefMap := map[string]interface{}{
		"apiVersion":         ownerRef.APIVersion,
		"kind":               ownerRef.Kind,
		"name":               ownerRef.Name,
		"uid":                string(ownerRef.UID),
		"controller":         ownerRef.Controller != nil && *ownerRef.Controller,
		"blockOwnerDeletion": ownerRef.BlockOwnerDeletion != nil && *ownerRef.BlockOwnerDeletion,
	}

	// Check if owner reference already exists
	for i, ref := range ownerRefs {
		if refMap, ok := ref.(map[string]interface{}); ok {
			if refMap["kind"] == ownerRef.Kind && refMap["name"] == ownerRef.Name {
				// Update existing reference
				ownerRefs[i] = ownerRefMap
				unstructured.SetNestedSlice(helmRelease.Object, ownerRefs, "metadata", "ownerReferences")
				return nil
			}
		}
	}

	// Add new owner reference
	ownerRefs = append(ownerRefs, ownerRefMap)
	unstructured.SetNestedSlice(helmRelease.Object, ownerRefs, "metadata", "ownerReferences")

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	clusterGVK := schema.GroupVersionKind{
		Group:   clusterAPIGroup,
		Version: clusterAPIVersion,
		Kind:    clusterKind,
	}

	// Create an unstructured object with the Cluster GVK for watching
	clusterObj := &unstructured.Unstructured{}
	clusterObj.SetGroupVersionKind(clusterGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(clusterObj).
		Named("cluster").
		Complete(r)
}
