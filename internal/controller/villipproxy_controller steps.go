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
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

const villipproxyFinalizer = "villip.carmier.fr/finalizer"

// Definitions to manage status conditions
const (
	// typeAvailableVillipProxy represents the status of the Deployment reconciliation
	typeAvailableVillipProxy = "Available"
	// typeDegradedVillipProxy represents the status used when the custom resource is deleted and the finalizer operations are yet to occur.
	typeDegradedVillipProxy = "Degraded"
)

func (r *VillipProxyReconciler) ManagePortChange(ctx context.Context, villipproxy *villipv1.VillipProxy, req ctrl.Request, found *appsv1.Deployment, sfound *v1.Service) (*reconcile.Result, error) {
	log := logf.FromContext(ctx)
	depports := portsForVillipProxyDeployment(villipproxy.Spec.Ports)
	depportsChanged := !reflect.DeepEqual(depports, found.Spec.Template.Spec.Containers[0].Ports)
	svcports := portsForVillipProxyService(villipproxy.Spec.Ports)
	svcportsChanged := !reflect.DeepEqual(svcports, sfound.Spec.Ports)

	if depportsChanged || svcportsChanged {
		if depportsChanged {
			log.Info("Ports for deployments are not aligned", "old", found.Spec.Template.Spec.Containers[0].Ports, "new", depports)
			found.Spec.Template.Spec.Containers[0].Ports = depports

			if err := r.deploymentUpdate(ctx, req, villipproxy, found); err != nil {
				return &ctrl.Result{}, err
			}
		}
		if svcportsChanged {
			log.Info("Ports for service are not aligned")
			sfound.Spec.Ports = svcports

			if err := r.serviceUpdate(ctx, req, villipproxy, sfound); err != nil {
				return &ctrl.Result{}, err
			}
		}
		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return &ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	return nil, nil
}

func (r *VillipProxyReconciler) villipProxyMarkedToBeDeleted(ctx context.Context, villipproxy *villipv1.VillipProxy, req ctrl.Request) error {
	log := logf.FromContext(ctx)
	if controllerutil.ContainsFinalizer(villipproxy, villipproxyFinalizer) {
		log.Info("Performing Finalizer Operations for VillipProxy before delete CR")

		// Let's add here a status "Downgrade" to reflect that this resource began its process to be terminated.
		meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeDegradedVillipProxy,
			Status: metav1.ConditionUnknown, Reason: "Finalizing",
			Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", villipproxy.Name)})

		if err := r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipProxy status")
			return err
		}

		// Perform all operations required before removing the finalizer and allow
		// the Kubernetes API to remove the custom resource.
		r.doFinalizerOperationsForVillipProxy(villipproxy)

		// TODO(user): If you add operations to the doFinalizerOperationsForVillipProxy method
		// then you need to ensure that all worked fine before deleting and updating the Downgrade status
		// otherwise, you should requeue here.

		// Re-fetch the villipproxy Custom Resource before updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, villipproxy); err != nil {
			log.Error(err, "Failed to re-fetch villipproxy")
			return err
		}

		meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeDegradedVillipProxy,
			Status: metav1.ConditionTrue, Reason: "Finalizing",
			Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", villipproxy.Name)})

		if err := r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipProxy status")
			return err
		}

		log.Info("Removing Finalizer for VillipProxy after successfully perform the operations")
		if ok := controllerutil.RemoveFinalizer(villipproxy, villipproxyFinalizer); !ok {
			err := fmt.Errorf("finalizer for VillipProxy was not removed")
			log.Error(err, "Failed to remove finalizer for VillipProxy")
			return err
		}

		if err := r.Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to remove finalizer for VillipProxy")
			return err
		}
	}
	return nil
}

// finalizeVillipProxy will perform the required operations before delete the CR.
func (r *VillipProxyReconciler) doFinalizerOperationsForVillipProxy(cr *villipv1.VillipProxy) {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.

	// Note: It is not recommended to use finalizers with the purpose of deleting resources which are
	// created and managed in the reconciliation. These ones, such as the Deployment created on this reconcile,
	// are defined as dependent of the custom resource. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the Deployment will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
}

func (r *VillipProxyReconciler) initStatus(ctx context.Context, villipproxy *villipv1.VillipProxy) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
	meta.SetStatusCondition(
		&villipproxy.Status.Conditions,
		metav1.Condition{
			Type:    typeAvailableVillipProxy,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"},
	)
	if err := r.Status().Update(ctx, villipproxy); err != nil {
		log.Error(err, "Failed to update VillipProxy status")
		return ctrl.Result{}, err
	}

	// Force the requeue to re-fetch the villipproxy Custom Resource after updating the status
	// so that we have the latest state of the resource on the cluster and we will avoid
	// raising the error "the object has been modified, please apply
	// your changes to the latest version and try again" which would re-trigger the reconciliation
	// if we try to update it again in the following operations
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *VillipProxyReconciler) addFinalizer(ctx context.Context, villipproxy *villipv1.VillipProxy) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Adding Finalizer for VillipProxy")
	if ok := controllerutil.AddFinalizer(villipproxy, villipproxyFinalizer); !ok {
		err := fmt.Errorf("finalizer for VillipProxy was not added")
		log.Error(err, "Failed to add finalizer for VillipProxy")
		return ctrl.Result{}, err
	}

	if err := r.Update(ctx, villipproxy); err != nil {
		log.Error(err, "Failed to update custom resource to add finalizer")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}
