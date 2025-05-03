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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

func (r *VillipProxyReconciler) deploymentUpdate(ctx context.Context, req ctrl.Request, villipproxy *villipv1.VillipProxy, dep *appsv1.Deployment) error {
	log := logf.FromContext(ctx)
	if err := r.Update(ctx, dep); err != nil {
		log.Error(err, "Failed to update Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

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
			Message: fmt.Sprintf("Failed to update the deployment for custom resource (%s): (%s)", villipproxy.Name, err)})

		if err := r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipProxy status")
			return err
		}
	}
	return nil
}

// deploymentForVillipProxy returns a VillipProxy Deployment object
func (r *VillipProxyReconciler) deploymentForVillipProxy(ctx context.Context, proxy *villipv1.VillipProxy) (*appsv1.Deployment, error) {
	ls := labelsForVillipProxy(proxy)
	replicas := proxy.Spec.Size

	target := targetForVillipProxy(proxy.Spec.Target)
	ports := portsForVillipProxyDeployment(proxy.Spec.Ports)
	vmounts, volumes, annotations, err := r.mountsForVillipProxy(ctx, proxy.Spec.Ruleset, proxy.Namespace)
	if err != nil {
		return nil, err
	}

	// Get the Operand image
	image := imageForVillipProxy(proxy)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        proxy.Name,
			Namespace:   proxy.Namespace,
			Labels:      ls,
			Annotations: annotations,
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

func portsForVillipProxyDeployment(pports []uint16) []v1.ContainerPort {
	ports := make([]v1.ContainerPort, 0, len(pports))
	for _, port := range sortedPortsOfVillipProxy(pports) {
		ports = append(ports, v1.ContainerPort{
			ContainerPort: int32(port),
			Protocol:      "TCP",
		})
	}
	return ports
}

func (r *VillipProxyReconciler) mountsForVillipProxy(ctx context.Context, rulesets []villipv1.VillipProxyRuleset, namespace string) ([]v1.VolumeMount, []v1.Volume, map[string]string, error) {
	log := logf.FromContext(ctx)
	vmounts := make([]v1.VolumeMount, 0)
	volumes := make([]v1.Volume, 0)
	cmNames := make([]string, 0)
	annotations := make(map[string]string)

	for _, ruleset := range rulesets {
		selector := ruleset.MatchLabels
		var confCM v1.ConfigMapList
		if err := r.List(ctx, &confCM, client.InNamespace(namespace), selector); err != nil {
			log.Error(err, "Unable to retrieve configMap list")
			return vmounts, volumes, annotations, err
		}

		for _, cm := range confCM.Items {
			cmNames = append(cmNames, cm.Name)
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
	sort.Strings(cmNames)
	annotations["villip.carmier.fr/conf"] = strings.Join(cmNames, ",")

	return vmounts, volumes, annotations, nil
}

func (r *VillipProxyReconciler) isRulesetsChanged(ctx context.Context, proxy villipv1.VillipProxy, dep *appsv1.Deployment) (bool, error) {
	log := logf.FromContext(ctx)
	cmNames := make([]string, 0)

	for _, ruleset := range proxy.Spec.Ruleset {
		selector := ruleset.MatchLabels
		var confCM v1.ConfigMapList
		if err := r.List(ctx, &confCM, client.InNamespace(proxy.Namespace), selector); err != nil {
			log.Error(err, "Unable to retrieve configMap list")
			return false, err
		}

		for _, cm := range confCM.Items {
			cmNames = append(cmNames, cm.Name)
		}
	}
	sort.Strings(cmNames)
	selected := strings.Join(cmNames, ",")
	if annotation, ok := dep.ObjectMeta.Annotations["villip.carmier.fr/conf"]; ok {
		return annotation != selected, nil
	}
	return false, nil
}

// labelsForVillipProxy returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForVillipProxy(proxy *villipv1.VillipProxy) map[string]string {
	image := imageForVillipProxy(proxy)
	imageTag := strings.Split(image, ":")[1]

	labels := map[string]string{
		"app.kubernetes.io/name":       proxy.Name,
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
func imageForVillipProxy(proxy *villipv1.VillipProxy) string {
	/*
		 For testing purpose the image is defined in the proxy spec
			 var imageEnvVar = "VILLIPPROXY_IMAGE"
			 image, found := os.LookupEnv(imageEnvVar)
			 if !found {
			 	return "", fmt.Errorf("Unable to find %s environment variable with the image", imageEnvVar)
			 }
			 return image, nil
	*/
	return fmt.Sprintf("%s:%s", proxy.Spec.Image.Name, proxy.Spec.Image.Tag)
}

func (r *VillipProxyReconciler) createDeployment(ctx context.Context, villipproxy *villipv1.VillipProxy) (reconcile.Result, error) {
	log := logf.FromContext(ctx)
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
		// The following implementation will update the status
		meta.SetStatusCondition(&villipproxy.Status.Conditions, metav1.Condition{Type: typeAvailableVillipRules,
			Status: metav1.ConditionFalse, Reason: "Reconciling",
			Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", villipproxy.Name, err)})

		if err := r.Status().Update(ctx, villipproxy); err != nil {
			log.Error(err, "Failed to update VillipRules status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Deployment created successfully
	// We will requeue the reconciliation so that we can ensure the state
	// and move forward for the next operations
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}
