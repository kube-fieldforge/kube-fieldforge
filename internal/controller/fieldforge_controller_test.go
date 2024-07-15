/*
Copyright 2024 Patrick Uiterwijk <patrick@puiterwijk.org>.

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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fieldforgev1alpha1 "github.com/kube-fieldforge/kube-fieldforge/api/v1alpha1"
)

func cleanConditions(fforge *fieldforgev1alpha1.FieldForge) {
	for i := 0; i < len(fforge.Status.Conditions); i++ {
		fforge.Status.Conditions[i].ObservedGeneration = 0
		fforge.Status.Conditions[i].LastTransitionTime = metav1.Time{}
	}
}

var _ = Describe("FieldForge Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		typeNamespacedTargetName := types.NamespacedName{
			Name:      "test-target",
			Namespace: "default",
		}
		typeNamespacedConfigMapName := types.NamespacedName{
			Name:      "test-config-map",
			Namespace: "default",
		}
		typeNamespacedSecretName := types.NamespacedName{
			Name:      "test-secret",
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind FieldForge")
			fieldforge := &fieldforgev1alpha1.FieldForge{}
			err := k8sClient.Get(ctx, typeNamespacedName, fieldforge)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue())

			immutable := true

			cm_resource := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedConfigMapName.Name,
					Namespace: typeNamespacedConfigMapName.Namespace,
				},
				Immutable: &immutable,
				Data: map[string]string{
					"cm_string_key": "cm_string_value",
				},
				BinaryData: map[string][]byte{
					"cm_binary_key": []byte{0x1, 0x2},
				},
			}
			Expect(k8sClient.Create(ctx, cm_resource)).To(Succeed())
			s_resource := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedSecretName.Name,
					Namespace: typeNamespacedSecretName.Namespace,
				},
				Immutable: &immutable,
				Data: map[string][]byte{
					"s_binary_key": []byte{0x3, 0x4},
				},
				StringData: map[string]string{
					"s_string_key": "s_string_value",
				},
			}
			Expect(k8sClient.Create(ctx, s_resource)).To(Succeed())
		})

		AfterEach(func() {
			resource := &fieldforgev1alpha1.FieldForge{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance FieldForge")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			cm_target_resource := &v1.ConfigMap{}
			if err = k8sClient.Get(ctx, typeNamespacedTargetName, cm_target_resource); err == nil {
				Expect(k8sClient.Delete(ctx, cm_target_resource)).To(Succeed())
			}
			s_target_resource := &v1.Secret{}
			if err = k8sClient.Get(ctx, typeNamespacedSecretName, s_target_resource); err == nil {
				Expect(k8sClient.Delete(ctx, s_target_resource)).To(Succeed())
			}

			cm_resource := &v1.ConfigMap{}
			if err := k8sClient.Get(ctx, typeNamespacedConfigMapName, cm_resource); err == nil {
				Expect(k8sClient.Delete(ctx, cm_resource)).To(Succeed())
			}
			s_resource := &v1.Secret{}
			if err := k8sClient.Get(ctx, typeNamespacedSecretName, s_resource); err == nil {
				Expect(k8sClient.Delete(ctx, s_resource)).To(Succeed())
			}
		})

		It("should successfully reconcile a full resource", func() {
			By("Reconciling a full, correct resource")

			resource := &fieldforgev1alpha1.FieldForge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: fieldforgev1alpha1.FieldForgeSpec{
					Target: fieldforgev1alpha1.TargetSpec{
						Kind: "configmap",
						Name: typeNamespacedTargetName.Name,
					},
					Annotations: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"test_annotation": fieldforgev1alpha1.FieldForgeSpecReference{
							Type: "constant",
							Constant: fieldforgev1alpha1.ConstantValue{
								Value: "constant_annotation",
							},
						},
					},
					Labels: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"test_label": fieldforgev1alpha1.FieldForgeSpecReference{
							Type: "configmap",
							ConfigMap: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedConfigMapName.Name,
								Key:  "cm_string_key",
							},
						},
					},
					StringData: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"string_from_secret": fieldforgev1alpha1.FieldForgeSpecReference{
							Type: "secret",
							Secret: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedSecretName.Name,
								Key:  "s_string_key",
							},
						},
					},
					BinaryData: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"binary_from_cm": fieldforgev1alpha1.FieldForgeSpecReference{
							Type: "configmap",
							ConfigMap: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedConfigMapName.Name,
								Key:  "cm_binary_key",
							},
						},
						"binary_from_secret": fieldforgev1alpha1.FieldForgeSpecReference{
							Type: "secret",
							Secret: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedSecretName.Name,
								Key:  "s_binary_key",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			recorder := record.NewFakeRecorder(50)
			controllerReconciler := &FieldForgeReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			res, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{Requeue: true, RequeueAfter: UpdateTime}))

			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			cleanConditions(resource)

			Expect(resource.Status.Conditions).To(HaveLen(2))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "GotSources",
				Status:  metav1.ConditionTrue,
				Reason:  "SourcesRetrieved",
				Message: "Sources retrieved successfully",
			}))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "UpdatedTarget",
				Status:  metav1.ConditionTrue,
				Reason:  "TargetUpdated",
				Message: "Target object successfully updated",
			}))

			event := <-recorder.Events
			Expect(event).To(Equal("Normal UpdatedTarget Updated target object"))

			target := &v1.ConfigMap{}
			err = k8sClient.Get(ctx, typeNamespacedTargetName, target)
			Expect(err).NotTo(HaveOccurred())

			Expect(target.Labels).To(HaveKeyWithValue("test_label", "cm_string_value"))
			Expect(target.Annotations).To(HaveKeyWithValue("test_annotation", "constant_annotation"))
			Expect(target.Data).To(HaveKeyWithValue("string_from_secret", "s_string_value"))
			Expect(target.BinaryData).To(HaveKeyWithValue("binary_from_cm", []byte{0x1, 0x2}))
			Expect(target.BinaryData).To(HaveKeyWithValue("binary_from_secret", []byte{0x3, 0x4}))
		})

		It("should successfully reconcile a resource without types", func() {
			By("Reconciling a full, correct resource without specified types")

			resource := &fieldforgev1alpha1.FieldForge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: fieldforgev1alpha1.FieldForgeSpec{
					Target: fieldforgev1alpha1.TargetSpec{
						Kind: "configmap",
						Name: typeNamespacedTargetName.Name,
					},
					Annotations: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"test_annotation": fieldforgev1alpha1.FieldForgeSpecReference{
							Constant: fieldforgev1alpha1.ConstantValue{
								Value: "constant_annotation",
							},
						},
					},
					Labels: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"test_label": fieldforgev1alpha1.FieldForgeSpecReference{
							ConfigMap: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedConfigMapName.Name,
								Key:  "cm_string_key",
							},
						},
					},
					StringData: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"string_from_secret": fieldforgev1alpha1.FieldForgeSpecReference{
							Secret: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedSecretName.Name,
								Key:  "s_string_key",
							},
						},
					},
					BinaryData: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"binary_from_cm": fieldforgev1alpha1.FieldForgeSpecReference{
							ConfigMap: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedConfigMapName.Name,
								Key:  "cm_binary_key",
							},
						},
						"binary_from_secret": fieldforgev1alpha1.FieldForgeSpecReference{
							Secret: fieldforgev1alpha1.ObjectValue{
								Name: typeNamespacedSecretName.Name,
								Key:  "s_binary_key",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			recorder := record.NewFakeRecorder(50)
			controllerReconciler := &FieldForgeReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			res, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{Requeue: true, RequeueAfter: UpdateTime}))

			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			cleanConditions(resource)

			Expect(resource.Status.Conditions).To(HaveLen(2))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "GotSources",
				Status:  metav1.ConditionTrue,
				Reason:  "SourcesRetrieved",
				Message: "Sources retrieved successfully",
			}))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "UpdatedTarget",
				Status:  metav1.ConditionTrue,
				Reason:  "TargetUpdated",
				Message: "Target object successfully updated",
			}))

			event := <-recorder.Events
			Expect(event).To(Equal("Normal UpdatedTarget Updated target object"))

			target := &v1.ConfigMap{}
			err = k8sClient.Get(ctx, typeNamespacedTargetName, target)
			Expect(err).NotTo(HaveOccurred())

			Expect(target.Labels).To(HaveKeyWithValue("test_label", "cm_string_value"))
			Expect(target.Annotations).To(HaveKeyWithValue("test_annotation", "constant_annotation"))
			Expect(target.Data).To(HaveKeyWithValue("string_from_secret", "s_string_value"))
			Expect(target.BinaryData).To(HaveKeyWithValue("binary_from_cm", []byte{0x1, 0x2}))
			Expect(target.BinaryData).To(HaveKeyWithValue("binary_from_secret", []byte{0x3, 0x4}))
		})

		It("should requeue for unknown sources", func() {
			resource := &fieldforgev1alpha1.FieldForge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: fieldforgev1alpha1.FieldForgeSpec{
					Target: fieldforgev1alpha1.TargetSpec{
						Kind: "configmap",
						Name: typeNamespacedTargetName.Name,
					},
					StringData: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"data": fieldforgev1alpha1.FieldForgeSpecReference{
							ConfigMap: fieldforgev1alpha1.ObjectValue{
								Name: "non-existant-secret",
								Key:  "s_string_key",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			recorder := record.NewFakeRecorder(50)
			controllerReconciler := &FieldForgeReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			res, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{Requeue: true, RequeueAfter: UpdateTime}))

			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			cleanConditions(resource)

			Expect(resource.Status.Conditions).To(HaveLen(2))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "GotSources",
				Status:  metav1.ConditionFalse,
				Reason:  "SourceNotFound",
				Message: "object default/non-existant-secret referenced in stringData data not found",
			}))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "UpdatedTarget",
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			}))
		})

		It("should not requeue for invalid source type", func() {
			resource := &fieldforgev1alpha1.FieldForge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: fieldforgev1alpha1.FieldForgeSpec{
					Target: fieldforgev1alpha1.TargetSpec{
						Kind: "configmap",
						Name: typeNamespacedTargetName.Name,
					},
					StringData: map[string]fieldforgev1alpha1.FieldForgeSpecReference{
						"data": fieldforgev1alpha1.FieldForgeSpecReference{
							Type: "foo",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			recorder := record.NewFakeRecorder(50)
			controllerReconciler := &FieldForgeReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			res, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{Requeue: false}))

			err = k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			cleanConditions(resource)

			Expect(resource.Status.Conditions).To(HaveLen(2))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "GotSources",
				Status:  metav1.ConditionFalse,
				Reason:  "ErrorRetrievingSource",
				Message: "error retriving value referenced in stringData data: invalid type foo (object <nil>)",
			}))
			Expect(resource.Status.Conditions).To(ContainElement(metav1.Condition{
				Type:    "UpdatedTarget",
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			}))
		})
	})
})
