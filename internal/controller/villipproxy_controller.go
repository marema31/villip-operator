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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
		meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipProxy, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipProxy status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the villipproxy Custom Resource after updating the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raising the error "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, villipproxy); err != nil {
			log.Error(err, "Failed to re-fetch villipproxy")
			return ctrl.Result{}, err
		}
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occur before the custom resource is deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(villipproxy, villipproxyFinalizer) {
		log.Info("Adding Finalizer for VillipProxy")
		if ok := controllerutil.AddFinalizer(villipproxy, villipproxyFinalizer); !ok {
			err = fmt.Errorf("finalizer for VillipProxy was not added")
			log.Error(err, "Failed to add finalizer for VillipProxy")
			return ctrl.Result{}, err
		}

		if err = r.Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Check if the VillipProxy instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isVillipProxyMarkedToBeDeleted := villipproxy.GetDeletionTimestamp() != nil
	if isVillipProxyMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(villipproxy, villipproxyFinalizer) {
			log.Info("Performing Finalizer Operations for VillipProxy before delete CR")

			// Let's add here a status "Downgrade" to reflect that this resource began its process to be terminated.
			meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeDegradedVillipProxy,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", villipproxy.Name)})

			if err := r.Status().Update(ctx, villipproxy); err != nil {
				log.Error(err, "Failed to update VillipProxy status")
				return ctrl.Result{}, err
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
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeDegradedVillipProxy,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", villipproxy.Name)})

			if err := r.Status().Update(ctx, villipproxy); err != nil {
				log.Error(err, "Failed to update VillipProxy status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for VillipProxy after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(villipproxy, villipproxyFinalizer); !ok {
				err = fmt.Errorf("finalizer for VillipProxy was not removed")
				log.Error(err, "Failed to remove finalizer for VillipProxy")
				return ctrl.Result{}, err
			}

			if err := r.Update(ctx, villipproxy); err != nil {
				log.Error(err, "Failed to remove finalizer for VillipProxy")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: villipproxy.Name, Namespace: villipproxy.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		dep, err := r.deploymentForVillipProxy(ctx, villipproxy)
		if err != nil {
			log.Error(err, "Failed to define new Deployment resource for VillipProxy")

			// The following implementation will update the status
			meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipProxy,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", villipproxy.Name, err)})

			if err := r.Status().Update(ctx, villipproxy); err != nil {
				log.Error(err, "Failed to update VillipProxy status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
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
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the villipproxy Custom Resource before updating the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raising the error "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, villipproxy); err != nil {
				log.Error(err, "Failed to re-fetch villipproxy")
				return ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipProxy,
				Status: metav1.ConditionFalse, Reason: "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", villipproxy.Name, err)})

			if err := r.Status().Update(ctx, villipproxy); err != nil {
				log.Error(err, "Failed to update VillipProxy status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the service already exists, if not create a new one
	sfounc := &v1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: villipproxy.Name, Namespace: villipproxy.Namespace}, sfounc)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new service
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
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		// Let's return the error for the reconciliation be re-triggered again
		return ctrl.Result{}, err
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

// deploymentForVillipProxy returns a VillipProxy Deployment object
func (r *VillipProxyReconciler) deploymentForVillipProxy(ctx context.Context, proxy *villipv1.VillipProxy) (*appsv1.Deployment, error) {
	ls := labelsForVillipProxy(proxy)
	replicas := proxy.Spec.Size

	target := targetForVillipProxy(proxy.Spec.Target)
	ports := portsForVillipProxy(proxy.Spec.Ports)
	vmounts, volumes, err := r.mountsForVillipProxy(ctx, proxy.Spec.Ruleset, proxy.Namespace)
	if err != nil {
		return nil, err
	}

	// Get the Operand image
	image, err := imageForVillipProxy(proxy)
	if err != nil {
		return nil, err
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					// Affinity: &corev1.Affinity{
					//	 NodeAffinity: &corev1.NodeAffinity{
					//		 RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			 NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				 {
					//					 MatchExpressions: []corev1.NodeSelectorRequirement{
					//						 {
					//							 Key:      "kubernetes.io/arch",
					//							 Operator: "In",
					//							 Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						 },
					//						 {
					//							 Key:      "kubernetes.io/os",
					//							 Operator: "In",
					//							 Values:   []string{"linux"},
					//						 },
					//					 },
					//				 },
					//		 	 },
					//		 },
					//	 },
					// },
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Volumes: volumes,
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "villip",
						ImagePullPolicy: corev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             ptr.To(true),
							RunAsUser:                ptr.To(int64(1001)),
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Env: []v1.EnvVar{
							{
								Name:  "VILLIP_FOLDER",
								Value: "/conf",
							},
							{
								Name:  "VILLIP_FOLDER_RECURSE",
								Value: "1",
							},
							{
								Name:  "VILLIP_URL",
								Value: target,
							},
						},
						Ports:        ports,
						VolumeMounts: vmounts,
						LivenessProbe: &v1.Probe{
							ProbeHandler: v1.ProbeHandler{
								HTTPGet: &v1.HTTPGetAction{
									Port: intstr.FromInt(9000),
								},
							},
							InitialDelaySeconds: 10,
							PeriodSeconds:       2,
						},
						ReadinessProbe: &v1.Probe{
							ProbeHandler: v1.ProbeHandler{
								HTTPGet: &v1.HTTPGetAction{
									Port: intstr.FromInt(9000),
								},
							},
							InitialDelaySeconds: 10,
							PeriodSeconds:       2,
						},
					}},
				},
			},
		},
	}

	if proxy.Spec.Debug {
		dep.Spec.Template.Spec.Containers[0].Env = append(
			dep.Spec.Template.Spec.Containers[0].Env,
			v1.EnvVar{
				Name:  "VILLIP_DEBUG",
				Value: "1",
			},
		)
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(proxy, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// targetForVillipProxy returns the target of proxy
func targetForVillipProxy(ptarget villipv1.VillipProxyTarget) string {
	return fmt.Sprintf("http://%s.%s.:%d%s",
		ptarget.Name,
		ptarget.Namespace,
		ptarget.Port,
		ptarget.Uri,
	)
}

func portsForVillipProxy(pports []uint16) []v1.ContainerPort {
	ports := make([]v1.ContainerPort, 0, len(pports))
	for _, port := range pports {
		ports = append(ports, v1.ContainerPort{
			ContainerPort: int32(port),
		})
	}
	return ports
}

func (r *VillipProxyReconciler) mountsForVillipProxy(ctx context.Context, rulesets []villipv1.VillipProxyRuleset, namespace string) ([]v1.VolumeMount, []v1.Volume, error) {
	log := logf.FromContext(ctx)
	vmounts := make([]v1.VolumeMount, 0)
	volumes := make([]v1.Volume, 0)

	for _, ruleset := range rulesets {
		selector := ruleset.MatchLabels
		var confCM v1.ConfigMapList
		if err := r.List(ctx, &confCM, client.InNamespace(namespace), selector); err != nil {
			log.Error(err, "Unable to retrieve configMap list")
			return vmounts, volumes, err
		}
		for _, cm := range confCM.Items {
			vmounts = append(vmounts, v1.VolumeMount{
				Name:      cm.Name,
				MountPath: fmt.Sprintf("/conf/%s", cm.Name),
			})
			volumes = append(volumes, v1.Volume{
				Name: cm.Name,
				VolumeSource: v1.VolumeSource{
					ConfigMap: &v1.ConfigMapVolumeSource{
						LocalObjectReference: v1.LocalObjectReference{
							Name: cm.Name,
						},
					},
				},
			})
		}
	}

	return vmounts, volumes, nil
}

// labelsForVillipProxy returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForVillipProxy(proxy *villipv1.VillipProxy) map[string]string {
	var imageTag string
	image, err := imageForVillipProxy(proxy)
	if err == nil {
		imageTag = strings.Split(image, ":")[1]
	}
	labels := map[string]string{
		"app.kubernetes.io/name":       "villip-operator",
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/managed-by": "VillipProxyController",
	}

	for k, v := range proxy.Labels {
		labels[k] = v
	}

	return labels
}

// imageForVillipProxy gets the Operand image which is managed by this controller
// from the VILLIPPROXY_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForVillipProxy(proxy *villipv1.VillipProxy) (string, error) {
	/*
		 For testing purpose the image is defined in the proxy spec
			 var imageEnvVar = "VILLIPPROXY_IMAGE"
			 image, found := os.LookupEnv(imageEnvVar)
			 if !found {
			 	return "", fmt.Errorf("Unable to find %s environment variable with the image", imageEnvVar)
			 }
			 return image, nil
	*/
	return fmt.Sprintf("%s:%s", proxy.Spec.Image.Name, proxy.Spec.Image.Tag), nil
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
			Ports: []v1.ServicePort{},
			Selector: map[string]string{
				"villip": proxy.Name,
			},
		},
	}

	for _, port := range proxy.Spec.Ports {
		srv.Spec.Ports = append(srv.Spec.Ports, v1.ServicePort{
			Protocol:   "TCP",
			Port:       int32(port),
			TargetPort: intstr.FromInt(int(port)),
			Name:       fmt.Sprintf("port%d", port),
		})
	}
	// Add reference of the proxy to the service to trigger reconcile on event on the service

	// Set the ownerRef for the Service
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(proxy, srv, r.Scheme); err != nil {
		return nil, err
	}
	return srv, nil
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
