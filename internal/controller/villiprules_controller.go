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
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villiprules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villiprules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=villip.carmier.fr,resources=villiprules/finalizers,verbs=update

// VillipRules create configMap, allow the controller to manage them
// +kubebuilder:rbac:groups=*,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

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
		return r.initStatus(ctx, rule)
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occur before the custom resource is deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(rule, villiprulesFinalizer) {
		return r.addFinalizer(ctx, rule)
	}

	// Check if the VillipRules instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isVillipRulesMarkedToBeDeleted := rule.GetDeletionTimestamp() != nil
	if isVillipRulesMarkedToBeDeleted {
		err := r.villipRulesMarkedToBeDeleted(ctx, rule, req)
		return ctrl.Result{}, err
	}

	// Check if the ConfigMaps already exists,
	found := v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: rule.Name, Namespace: rule.Namespace}, &found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new Configmap
		return r.createConfigMap(ctx, rule)

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
		found.Data = map[string]string{
			rule.Name + ".yml": string(data),
		}
		return r.updateConfigMap(ctx, req, rule, &found, sumRule)
	}
	// Requeue the request to ensure the ConfigMap is created
	return ctrl.Result{RequeueAfter: time.Minute}, nil
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
