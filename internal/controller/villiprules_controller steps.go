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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

const villiprulesFinalizer = "villip.carmier.fr/finalizer"

// Definitions to manage status conditions
const (
	// typeAvailableVillipRules represents the status of the Deployment reconciliation
	typeAvailableVillipRules = "Available"
	// typeDegradedVillipRules represents the status used when the custom resource is deleted and the finalizer operations are yet to occur.
	typeDegradedVillipRules = "Degraded"
)

func (r *VillipRulesReconciler) villipRulesMarkedToBeDeleted(ctx context.Context, villiprules *villipv1.VillipRules, req ctrl.Request) error {
	log := logf.FromContext(ctx)
	if controllerutil.ContainsFinalizer(villiprules, villiprulesFinalizer) {
		log.Info("Performing Finalizer Operations for VillipRules before delete CR")
		// Perform all operations required before removing the finalizer and allow
		// the Kubernetes API to remove the custom resource.
		r.doFinalizerOperationsForVillipRules(villiprules)

		// TODO(user): If you add operations to the doFinalizerOperationsForVillipRules method
		// then you need to ensure that all worked fine before deleting and updating the Downgrade status
		// otherwise, you should requeue here.

		// Re-fetch the villiprules Custom Resource before updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, villiprules); err != nil {
			log.Error(err, "Failed to re-fetch villiprules")
			return err
		}

		log.Info("Removing Finalizer for VillipRules after successfully perform the operations")
		f := villiprules.GetFinalizers()
		for i := range f {
			p := []byte(fmt.Sprintf(`[{"op": "remove", "path": "/metadata/finalizers/%d"}]`, i))
			patch := client.RawPatch(types.JSONPatchType, p)
			if err := r.Patch(ctx, villiprules, patch); err != nil {
				return err
			}
		}
	}
	return nil
}

// finalizeVillipRules will perform the required operations before delete the CR.
func (r *VillipRulesReconciler) doFinalizerOperationsForVillipRules(cr *villipv1.VillipRules) {
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

func (r *VillipRulesReconciler) initStatus(ctx context.Context, villiprules *villipv1.VillipRules) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
	meta.SetStatusCondition(
		&villiprules.Status.Conditions,
		metav1.Condition{
			Type:    typeAvailableVillipRules,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation"},
	)
	if err := r.Status().Update(ctx, villiprules); err != nil {
		log.Error(err, "Failed to update VillipRules status")
		return ctrl.Result{}, err
	}

	// Force the requeue to re-fetch the villiprules Custom Resource after updating the status
	// so that we have the latest state of the resource on the cluster and we will avoid
	// raising the error "the object has been modified, please apply
	// your changes to the latest version and try again" which would re-trigger the reconciliation
	// if we try to update it again in the following operations
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *VillipRulesReconciler) addFinalizer(ctx context.Context, villiprules *villipv1.VillipRules) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Adding Finalizer for VillipRules")
	if ok := controllerutil.AddFinalizer(villiprules, villiprulesFinalizer); !ok {
		err := fmt.Errorf("finalizer for VillipRules was not added")
		log.Error(err, "Failed to add finalizer for VillipRules")
		return ctrl.Result{}, err
	}

	if err := r.Update(ctx, villiprules); err != nil {
		log.Error(err, "Failed to update custom resource to add finalizer")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}
