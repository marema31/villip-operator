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
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

func (r *VillipProxyReconciler) serviceUpdate(ctx context.Context, req ctrl.Request, villipproxy *villipv1.VillipProxy, svc *v1.Service) error {
	log := logf.FromContext(ctx)
	if err := r.Update(ctx, svc); err != nil {
		log.Error(err, "Failed to update Service",
			"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)

		// Re-fetch the villipproxy Custom Resource before updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		if err := r.Get(ctx, req.NamespacedName, villipproxy); err != nil {
			log.Error(err, "Failed to re-fetch villipproxy")
			return err
		}

		// The following implementation will update the status
		meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipProxy,
			Status: metav1.ConditionFalse, Reason: "Reconciling",
			Message: fmt.Sprintf("Failed to update the Service for custom resource (%s): (%s)", villipproxy.Name, err)})

		if err := r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipProxy status")
			return err
		}
	}
	return nil
}

func sortedPortsOfVillipProxy(ports []uint16) []int {
	sorted := make([]int, 0, len(ports))
	for _, v := range ports {
		sorted = append(sorted, int(v))
	}
	sort.Ints(sorted)
	return sorted
}

func portsForVillipProxyService(pports []uint16) []v1.ServicePort {
	ports := make([]v1.ServicePort, 0, len(pports))
	for _, port := range sortedPortsOfVillipProxy(pports) {
		ports = append(ports, v1.ServicePort{
			Protocol:   "TCP",
			Port:       int32(port),
			TargetPort: intstr.FromInt(port),
			Name:       fmt.Sprintf("port%d", port),
		})
	}
	return ports
}

func (r *VillipProxyReconciler) serviceForVillipProxy(_ context.Context, proxy *villipv1.VillipProxy) (*v1.Service, error) {
	labels := make(map[string]string)
	for k, v := range proxy.Labels {
		labels[k] = v
	}
	// Define the desired Service

	srv := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
			Labels:    labels,
		},

		Spec: v1.ServiceSpec{
			Ports: portsForVillipProxyService(proxy.Spec.Ports),
			Selector: map[string]string{
				"villip": proxy.Name,
			},
		},
	}
	// Add reference of the proxy to the service to trigger reconcile on event on the service

	// Set the ownerRef for the Service
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(proxy, srv, r.Scheme); err != nil {
		return nil, err
	}
	return srv, nil
}

func (r *VillipProxyReconciler) createService(ctx context.Context, villipproxy *villipv1.VillipProxy) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	svc, err := r.serviceForVillipProxy(ctx, villipproxy)
	if err != nil {
		log.Error(err, "Failed to define new Service resource for VillipProxy")

		// The following implementation will update the status
		meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipProxy,
			Status: metav1.ConditionFalse, Reason: "Reconciling",
			Message: fmt.Sprintf("Failed to create Service for the custom resource (%s): (%s)", villipproxy.Name, err)})

		if err := r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipProxy status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	log.Info("Creating a new Service",
		"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
	if err = r.Create(ctx, svc); err != nil {
		log.Error(err, "Failed to create new Service",
			"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		return ctrl.Result{}, err
	}

	// Service created successfully
	// We will requeue the reconciliation so that we can ensure the state
	// and move forward for the next operations
	return ctrl.Result{RequeueAfter: time.Minute}, nil

}
