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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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

const villiprulesFinalizer = "villip.carmier.fr/finalizer"

// Definitions to manage status conditions
const (
	// typeAvailableVillipRules represents the status of the ConfigMap reconciliation
	typeAvailableVillipRules = "Available"
	// typeDegradedVillipRules represents the status used when the custom resource is deleted and the finalizer operations are yet to occur.
	typeDegradedVillipRules = "Degraded"
)

// VillipRulesReconciler reconciles a VillipRules object
type VillipRulesReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

var (
	ownerKey = ".metadata.controller"
	apiGVStr = villipv1.GroupVersion.String()
)

func sumVillipRulesData(data string) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, strings.NewReader(data)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (r *VillipRulesReconciler) configMapForVillipRule(ctx context.Context, rule *villipv1.VillipRules) (*v1.ConfigMap, error) {
	log := logf.FromContext(ctx)

	cm := &v1.ConfigMap{}
	data, err := yaml.Marshal(rule.Spec)
	if err != nil {
		log.Error(err, "Failed to decode spec")
		return cm, err
	}

	labels := make(map[string]string)
	for k, v := range rule.Labels {
		labels[k] = v
	}
	// Define the desired ConfigMap
	cm.ObjectMeta = metav1.ObjectMeta{
		Name:      rule.Name,
		Namespace: rule.Namespace,
		Labels:    labels,
	}
	cm.Data = map[string]string{
		rule.Name + ".yml": string(data),
	}

	// Add reference of the rule to the configMap to trigger reconcile on event on the configMap
	if err := ctrl.SetControllerReference(rule, cm, r.Scheme); err != nil {
		return cm, err
	}

	return cm, nil
}

// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villiprules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villiprules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villiprules/finalizers,verbs=update

// VillipRules create configMap, allow the controller to manage them
//+kubebuilder:rbac:groups=*,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VillipRules object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *VillipRulesReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Get the rule instance
	rule := &villipv1.VillipRules{}
	if err := r.Get(ctx, req.NamespacedName, rule); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("villiprule resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get villiprule")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status is available
	if len(rule.Status.Conditions) == 0 {
		log.Info("Initialize status condition for VillipRules")
		meta.SetStatusCondition(
			&rule.Status.Conditions,
			metav1.Condition{
				Type: typeAvailableVillipRules, Status: metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation"})
		if err := r.Status().Update(ctx, rule); err != nil {
			log.Error(err, "Failed to update Villiprules status")
			return ctrl.Result{}, err
		}

		// Force the requeue to re-fetch the villiprules Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occur before the custom resource is deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(rule, villiprulesFinalizer) {
		log.Info("Adding Finalizer for VillipRules")
		if ok := controllerutil.AddFinalizer(rule, villiprulesFinalizer); !ok {
			err := fmt.Errorf("finalizer for VillipRules was not added")
			log.Error(err, "Failed to add finalizer for VillipRules")
			return ctrl.Result{}, err
		}

		if err := r.Update(ctx, rule); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Check if the VillipRules instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isVillipRulesMarkedToBeDeleted := rule.GetDeletionTimestamp() != nil
	if isVillipRulesMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(rule, villiprulesFinalizer) {
			log.Info("Performing Finalizer Operations for VillipRules before delete CR")

			// Let's add here a status "Downgrade" to reflect that this resource began its process to be terminated.
			meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{Type: typeDegradedVillipRules,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", rule.Name)})

			if err := r.Status().Update(ctx, rule); err != nil {
				log.Error(err, "Failed to update VillipRules status")
				return ctrl.Result{}, err
			}

			// Perform all operations required before removing the finalizer and allow
			// the Kubernetes API to remove the custom resource.
			r.doFinalizerOperationsForVillipRule(rule)

			// TODO(user): If you add operations to the doFinalizerOperationsForVillipRules method
			// then you need to ensure that all worked fine before deleting and updating the Downgrade status
			// otherwise, you should requeue here.

			// Re-fetch the villipRules Custom Resource before updating the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raising the error "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, rule); err != nil {
				log.Error(err, "Failed to re-fetch villiprules")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{Type: typeDegradedVillipRules,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", rule.Name)})

			if err := r.Status().Update(ctx, rule); err != nil {
				log.Error(err, "Failed to update VillipRules status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for VillipRules after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(rule, villiprulesFinalizer); !ok {
				err := fmt.Errorf("finalizer for VillipRules was not removed")
				log.Error(err, "Failed to remove finalizer for VillipRules")
				return ctrl.Result{}, err
			}

			if err := r.Update(ctx, rule); err != nil {
				log.Error(err, "Failed to remove finalizer for VillipRules")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Check if the ConfigMaps already exists,
	found := v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: rule.Name, Namespace: rule.Namespace}, &found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new Configmap
		cm, err := r.configMapForVillipRule(ctx, rule)
		if err != nil {
			return ctrl.Result{}, err
		}

		sum, err := sumVillipRulesData(cm.Data[rule.Name+".yml"])
		if err != nil {
			log.Error(err, "Failed to calculate VillipRules checksum")
			return ctrl.Result{}, err
		}

		log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		if err := r.Create(ctx, cm); err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)

			// The following implementation will update the status
			meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{Type: typeAvailableVillipRules,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Configmap for the custom resource (%s): (%s)", rule.Name, err)})
			/*
				if err := r.Status().Update(ctx, rule); err != nil {
					log.Error(err, "Failed to update VillipRules status")
					return ctrl.Result{}, err
				}
			*/return ctrl.Result{}, err
		}
		// Set villipRule owner of the configmap (it will be automatically deleted when villipRule is deleted)
		if err := controllerutil.SetControllerReference(rule, cm, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Storing checksum in status", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		rule.Status.LastSum = sum

		meta.SetStatusCondition(
			&rule.Status.Conditions,
			metav1.Condition{
				Type: typeAvailableVillipRules, Status: metav1.ConditionTrue,
				Reason:  "Reconciled",
				Message: "ConfigMap created"})
		if err := r.Status().Update(ctx, rule); err != nil {
			log.Error(err, "Failed to update Villiprules status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err

	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	}

	//  Lookup for VillipRule Spec and configmap inconsistencies
	data, err := yaml.Marshal(rule.Spec)
	if err != nil {
		log.Error(err, "Failed to decode villiprules spec")
		return ctrl.Result{}, err
	}

	sumRule, err := sumVillipRulesData(string(data))
	if err != nil {
		log.Error(err, "Failed to calculate VillipRules checksum")
		return ctrl.Result{}, err
	}
	sumFound, err := sumVillipRulesData(found.Data[rule.Name+".yml"])
	if err != nil {
		log.Error(err, "Failed to calculate VillipRules checksum")
		return ctrl.Result{}, err
	}

	// Rule and configmap differs update the configmap
	if sumFound != sumRule {
		log.Info("Updating ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)

		found.Data = map[string]string{
			rule.Name + ".yml": string(data),
		}
		meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{Type: typeDegradedVillipRules,
			Status: metav1.ConditionFalse, Reason: "Updating",
			Message: fmt.Sprintf("Update the data for the custom resource (%s)", rule.Name)})

		if err := r.Status().Update(ctx, rule); err != nil {
			log.Error(err, "Failed to update Villiprules status")
			return ctrl.Result{}, err
		}

		if err = r.Update(ctx, &found); err != nil {
			log.Error(err, "Failed to update ConfigMap",
				"ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)

			// Re-fetch the villiprules Custom Resource before updating the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raising the error "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, rule); err != nil {
				log.Error(err, "Failed to re-fetch villiprules")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{Type: typeAvailableVillipRules,
				Status: metav1.ConditionFalse, Reason: "Updating",
				Message: fmt.Sprintf("Failed to update the data for the custom resource (%s): (%s)", rule.Name, err)})

			if err := r.Status().Update(ctx, rule); err != nil {
				log.Error(err, "Failed to update Villiprules status")
				return ctrl.Result{}, err
			}
		}

		// VillipRule last calculed sum differ from the actual one, replace it
		if rule.Status.LastSum != sumRule {
			rule.Status.LastSum = sumRule
		}

		meta.SetStatusCondition(
			&rule.Status.Conditions,
			metav1.Condition{
				Type: typeAvailableVillipRules, Status: metav1.ConditionTrue,
				Reason:  "Reconciled",
				Message: "ConfigMap updated"})
		if err := r.Status().Update(ctx, rule); err != nil {
			log.Error(err, "Failed to update Villiprules status")
			return ctrl.Result{}, err
		}
	}

	// Requeue the request to ensure the ConfigMap is created
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// finalizeVillipRule will perform the required operations before delete the CR.
func (r *VillipRulesReconciler) doFinalizerOperationsForVillipRule(cr *villipv1.VillipRules) {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.

	// Note: It is not recommended to use finalizers with the purpose of deleting resources which are
	// created and managed in the reconciliation. These ones, such as the ConfigMap created on this reconcile,
	// are defined as dependent of the custom resource. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the ConfigMap will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
}

// SetupWithManager sets up the controller with the Manager.
func (r *VillipRulesReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1.ConfigMap{}, ownerKey, func(rawObj client.Object) []string {
		// grab the configmap object, extract the owner...
		cm := rawObj.(*v1.ConfigMap)
		owner := metav1.GetControllerOf(cm)
		if owner == nil {
			return nil
		}
		// ...make sure it's a VillipRule...
		if owner.APIVersion != apiGVStr || owner.Kind != "VillipRule" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&villipv1.VillipRules{}).
		Owns(&v1.ConfigMap{}).
		Named("villiprules").
		Complete(r)
}
