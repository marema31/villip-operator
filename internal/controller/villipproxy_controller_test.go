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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	villipv1 "github.com/marema31/villip-operator/api/v1"
)

var _ = Describe("VillipProxy controller", func() {
	Context("VillipProxy controller test", func() {

		const VillipProxyName = "test-villipproxy"

		ctx := context.Background()

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      VillipProxyName,
				Namespace: VillipProxyName,
			},
		}

		typeNamespacedName := types.NamespacedName{
			Name:      VillipProxyName,
			Namespace: VillipProxyName,
		}
		villipproxy := &villipv1.VillipProxy{}

		SetDefaultEventuallyTimeout(2 * time.Minute)
		SetDefaultEventuallyPollingInterval(time.Second)

		BeforeEach(func() {
			By("Creating the Namespace to perform the tests")
			err := k8sClient.Create(ctx, namespace)
			Expect(err).NotTo(HaveOccurred())

			By("Setting the Image ENV VAR which stores the Operand image")
			err = os.Setenv("VILLIPPROXY_IMAGE", "example.com/image:test")
			Expect(err).NotTo(HaveOccurred())

			By("creating the custom resource for the Kind VillipProxy")
			err = k8sClient.Get(ctx, typeNamespacedName, villipproxy)
			if err != nil && errors.IsNotFound(err) {
				// Let's mock our custom resource at the same way that we would
				// apply on the cluster the manifest under config/samples
				villipproxy := &villipv1.VillipProxy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      VillipProxyName,
						Namespace: namespace.Name,
					},
					Spec: villipv1.VillipProxySpec{
						Size: 1,
					},
				}

				err = k8sClient.Create(ctx, villipproxy)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		AfterEach(func() {
			By("removing the custom resource for the Kind VillipProxy")
			found := &villipv1.VillipProxy{}
			err := k8sClient.Get(ctx, typeNamespacedName, found)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Delete(context.TODO(), found)).To(Succeed())
			}).Should(Succeed())

			// TODO(user): Attention if you improve this code by adding other context test you MUST
			// be aware of the current delete namespace limitations.
			// More info: https://book.kubebuilder.io/reference/envtest.html#testing-considerations
			By("Deleting the Namespace to perform the tests")
			_ = k8sClient.Delete(ctx, namespace)

			By("Removing the Image ENV VAR which stores the Operand image")
			_ = os.Unsetenv("VILLIPPROXY_IMAGE")
		})

		It("should successfully reconcile a custom resource for VillipProxy", func() {
			By("Checking if the custom resource was successfully created")
			Eventually(func(g Gomega) {
				found := &villipv1.VillipProxy{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, found)).To(Succeed())
			}).Should(Succeed())

			By("Reconciling the custom resource created")
			villipproxyReconciler := &VillipProxyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := villipproxyReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Reconciling the custom resource again")
			_, err = villipproxyReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking the latest Status Condition added to the VillipProxy instance")
			Expect(k8sClient.Get(ctx, typeNamespacedName, villipproxy)).To(Succeed())
			conditions := []metav1.Condition{}
			Expect(villipproxy.Status.Conditions).To(ContainElement(
				HaveField("Type", Equal(typeAvailableVillipProxy)), &conditions))
			Expect(conditions).To(HaveLen(1), "Multiple conditions of type %s", typeAvailableVillipProxy)
			Expect(conditions[0].Reason).To(Equal("Reconciling"), "condition %s", typeAvailableVillipProxy)
		})
	})
})
