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
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

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

func (r *VillipRulesReconciler) createConfigMap(ctx context.Context, rule *villipv1.VillipRules) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
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

		if err := r.Status().Update(ctx, rule); err != nil {
			log.Error(err, "Failed to update VillipRules status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
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
}

func (r *VillipRulesReconciler) updateConfigMap(ctx context.Context, req ctrl.Request, rule *villipv1.VillipRules, found *v1.ConfigMap, sumRule string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.Info("Updating ConfigMap", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)

	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{Type: typeDegradedVillipRules,
		Status: metav1.ConditionFalse, Reason: "Updating",
		Message: fmt.Sprintf("Update the data for the custom resource (%s)", rule.Name)})

	if err := r.Status().Update(ctx, rule); err != nil {
		log.Error(err, "Failed to update Villiprules status")
		return ctrl.Result{}, err
	}

	if err := r.Update(ctx, found); err != nil {
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

	if err := r.rolloutDependentProxies(ctx, rule.Namespace, rule.Name); err != nil {
		log.Error(err, "Failed to rollout dependent proxies deployments")
		return ctrl.Result{}, err
	}
	// Requeue the request to ensure the ConfigMap is created
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *VillipRulesReconciler) rolloutDependentProxies(ctx context.Context, namespace string, name string) error {
	log := logf.FromContext(ctx)
	deplList := appsv1.DeploymentList{}
	labelSelector := "app.kubernetes.io/managed-by=VillipProxyController"

	if err := r.List(
		ctx,
		&deplList,
		&client.ListOptions{
			Namespace: namespace,
			Raw:       &metav1.ListOptions{LabelSelector: labelSelector},
		},
	); err != nil {
		return err
	}

	for _, dep := range deplList.Items {
		if dependOn, ok := dep.ObjectMeta.Annotations["villip.carmier.fr/conf"]; ok {
			for _, ruleset := range strings.Split(dependOn, ",") {
				if ruleset == name {
					log.Info("Rolling out deployment to update configuration", "namespace", namespace, "ruleset", name, "deployment", dep.Name)
					dep.Spec.Template.ObjectMeta.Annotations = map[string]string{"villip-operator/config-reloaded-at": time.Now().String()}
					if err := r.Update(ctx, &dep); err != nil {
						log.Error(err, "Failed to rollout Deployment", "Deployment.Namespace", namespace, "Deployment.Name", dep.Name)
					}
				}
			}
		}
	}
	return nil
}
