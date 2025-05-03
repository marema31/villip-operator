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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

// VillipProxyReconciler reconciles a VillipProxy object
type VillipProxyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// The following markers are used to generate the rules permissions (RBAC) on config/rbac using controller-gen
// when the command <make manifests> is executed.
// To know more about markers see: https://book.kubebuilder.io/reference/markers.html

// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villipproxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villipproxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villipproxies/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// It is essential for the controller's reconciliation loop to be idempotent. By following the Operator
// pattern you will create Controllers which provide a reconcile function
// responsible for synchronizing resources until the desired state is reached on the cluster.
// Breaking this recommendation goes against the design principles of controller-runtime.
// and may lead to unforeseen consequences such as resources becoming stuck and requiring manual intervention.
// For further info:
// - About Operator Pattern: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
// - About Controllers: https://kubernetes.io/docs/concepts/architecture/controller/
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *VillipProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the VillipProxy instance
	// The purpose is check if the Custom Resource for the Kind VillipProxy
	// is applied on the cluster if not we return nil to stop the reconciliation
	villipproxy := &villipv1.VillipProxy{}
	err := r.Get(ctx, req.NamespacedName, villipproxy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("villipproxy resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get villipproxy")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status is available
	if len(villipproxy.Status.Conditions) == 0 {
		return r.initStatus(ctx, villipproxy)

	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occur before the custom resource is deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(villipproxy, villipproxyFinalizer) {
		return r.addFinalizer(ctx, villipproxy)
	}

	// Check if the VillipProxy instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isVillipProxyMarkedToBeDeleted := villipproxy.GetDeletionTimestamp() != nil
	if isVillipProxyMarkedToBeDeleted {
		err := r.villipProxyMarkedToBeDeleted(ctx, villipproxy, req)
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: villipproxy.Name, Namespace: villipproxy.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		return r.createDeployment(ctx, villipproxy)

	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-triggered again
		return ctrl.Result{}, err
	}

	// The CRD API defines that the VillipProxy type have a VillipProxySpec.Size field
	// to set the quantity of Deployment instances to the desired state on the cluster.
	// Therefore, the following code will ensure the Deployment size is the same as defined
	// via the Size spec of the Custom Resource which we are reconciling.
	size := villipproxy.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		log.Info(fmt.Sprintf("Resizing to %d", villipproxy.Spec.Size))
		if err = r.deploymentUpdate(ctx, req, villipproxy, found); err != nil {
			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	rulesetsChanged, err := r.isRulesetsChanged(ctx, *villipproxy, found)
	if err != nil {
		log.Error(err, "Verifying volumes configuration for deployment")
	}
	if rulesetsChanged {
		log.Info("Ruleset has changed")
		vmounts, volumes, annotations, err := r.mountsForVillipProxy(ctx, villipproxy.Spec.Ruleset, villipproxy.Namespace)
		if err != nil {
			log.Error(err, "Getting the new volumes configuration for deployment")
		}
		found.ObjectMeta.Annotations = annotations
		found.Spec.Template.Spec.Volumes = volumes
		found.Spec.Template.Spec.Containers[0].VolumeMounts = vmounts

		if err = r.deploymentUpdate(ctx, req, villipproxy, found); err != nil {
			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Check if the service already exists, if not create a new one
	sfound := &v1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: villipproxy.Name, Namespace: villipproxy.Namespace}, sfound)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new service
		return r.createService(ctx, villipproxy)
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		// Let's return the error for the reconciliation be re-triggered again
		return ctrl.Result{}, err
	}

	if result, err := r.ManagePortChange(ctx, villipproxy, req, found, sfound); result != nil {
		return *result, err
	}

	// The following implementation will update the status
	meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipProxy,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Deployment for custom resource (%s) with %d replicas created successfully", villipproxy.Name, size)})

	if err := r.Status().Update(ctx, villipproxy); err != nil {
		log.Error(err, "Failed to update VillipProxy status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
// The whole idea is to be watching the resources that matter for the controller.
// When a resource that the controller is interested in changes, the Watch triggers
// the controller’s reconciliation loop, ensuring that the actual state of the resource
// matches the desired state as defined in the controller’s logic.
//
// Notice how we configured the Manager to monitor events such as the creation, update,
// or deletion of a Custom Resource (CR) of the VillipProxy kind, as well as any changes
// to the Deployment that the controller manages and owns.
func (r *VillipProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Watch the VillipProxy CR(s) and trigger reconciliation whenever it
		// is created, updated, or deleted
		For(&villipv1.VillipProxy{}).
		Named("villipproxy").
		// Watch the Deployment managed by the VillipProxyReconciler. If any changes occur to the Deployment
		// owned and managed by this controller, it will trigger reconciliation, ensuring that the cluster
		// state aligns with the desired state. See that the ownerRef was set when the Deployment was created.
		Owns(&v1.Service{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
