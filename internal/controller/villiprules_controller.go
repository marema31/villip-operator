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

	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

// VillipRulesReconciler reconciles a VillipRules object
type VillipRulesReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

var (
	cmOwnerKey = ".metadata.controller"
	apiGVStr   = villipv1.GroupVersion.String()
)

func (r *VillipRulesReconciler) configMapForVillipRule(ctx context.Context, rule *villipv1.VillipRules, cm *v1.ConfigMap) error {
	log := logf.FromContext(ctx)
	data, err := yaml.Marshal(rule.Spec)
	if err != nil {
		log.Error(err, "Failed to decode spec")
		return err
	}

	labels := make(map[string]string)
	for k, v := range rule.Labels {
		labels[k] = v
	}
	// Define the desired ConfigMap
	cm.ObjectMeta = metav1.ObjectMeta{
		Name:      "vo-" + rule.Name,
		Namespace: rule.Namespace,
		Labels:    labels,
	}
	cm.Data = map[string]string{
		rule.Name + ".yml": string(data),
	}

	// Add reference of the rule to the configMap to trigger reconcile on event on the configMap
	if err := ctrl.SetControllerReference(rule, cm, r.Scheme); err != nil {
		return err
	}

	return nil
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
	var rule villipv1.VillipRules
	if err := r.Get(ctx, req.NamespacedName, &rule); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// for debug
	log.Info("Reconcile called")
	// Check if the ConfigMaps already exists,
	cm := v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: "vo-" + rule.Name, Namespace: rule.Namespace}, &cm)
	if err == nil {
		// Update the configmap
		err := r.configMapForVillipRule(ctx, &rule, &cm)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Updating ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)

		if err := r.Update(ctx, &cm); err != nil {
			log.Error(err, "Failed to update ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
			return ctrl.Result{}, err
		}
	} else if apierrors.IsNotFound(err) {
		// Define a new Configmap
		err := r.configMapForVillipRule(ctx, &rule, &cm)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating a new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		if err := r.Create(ctx, &cm); err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
			return ctrl.Result{}, err
		}
		// Set villipRule owner of the configmap (it will be automatically deleted when villipRule is deleted)
		if err := controllerutil.SetControllerReference(&rule, &cm, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue the request to ensure the ConfigMap is created
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else {
		log.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VillipRulesReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1.ConfigMap{}, cmOwnerKey, func(rawObj client.Object) []string {
		// grab the job object, extract the owner...
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
