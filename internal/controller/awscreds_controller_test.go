/*
Copyright 2026.

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "github.com/aditya130103/infra-operator/api/v1alpha1"
)

var _ = Describe("AWSCreds Controller", func() {
	const (
		namespace    = "default"
		awsCredsName = "test-awscreds"
		secretName   = "test-aws-secret"
	)

	ctx := context.Background()
	namespacedName := types.NamespacedName{Name: awsCredsName, Namespace: namespace}

	newReconciler := func() *AWSCredsReconciler {
		return &AWSCredsReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	}

	createAWSCreds := func() {
		ac := &corev1alpha1.AWSCreds{
			ObjectMeta: metav1.ObjectMeta{Name: awsCredsName, Namespace: namespace},
			Spec: corev1alpha1.AWSCredsSpec{
				Credentials: corev1alpha1.CredentialsSpec{
					SecretRef: corev1alpha1.SecretRefSpec{
						Name:      secretName,
						Namespace: namespace,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, ac)).To(Succeed())
	}

	deleteAWSCreds := func() {
		ac := &corev1alpha1.AWSCreds{}
		err := k8sClient.Get(ctx, namespacedName, ac)
		if err == nil {
			Expect(k8sClient.Delete(ctx, ac)).To(Succeed())
		}
	}

	deleteSecret := func() {
		s := &corev1.Secret{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, s)
		if err == nil {
			Expect(k8sClient.Delete(ctx, s)).To(Succeed())
		}
	}

	reconcileAndGet := func() (*corev1alpha1.AWSCreds, error) {
		_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		if err != nil {
			return nil, err
		}
		ac := &corev1alpha1.AWSCreds{}
		Expect(k8sClient.Get(ctx, namespacedName, ac)).To(Succeed())
		return ac, nil
	}

	AfterEach(func() {
		deleteAWSCreds()
		deleteSecret()
	})

	Context("when the referenced Secret does not exist", func() {
		BeforeEach(func() { createAWSCreds() })

		It("sets phase=NotFound and Ready=False with SecretNotFound reason", func() {
			ac, err := reconcileAndGet()
			Expect(err).NotTo(HaveOccurred())
			Expect(ac.Status.Phase).To(Equal("NotFound"))
			Expect(ac.Status.Conditions).To(ContainElement(
				HaveField("Reason", "SecretNotFound"),
			))
		})
	})

	Context("when the Secret is missing AWS_ACCESS_KEY_ID", func() {
		BeforeEach(func() {
			createAWSCreds()
			s := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Data:       map[string][]byte{"AWS_SECRET_ACCESS_KEY": []byte("secret")},
			}
			Expect(k8sClient.Create(ctx, s)).To(Succeed())
		})

		It("sets phase=NotReady with SecretMissingField reason", func() {
			ac, err := reconcileAndGet()
			Expect(err).NotTo(HaveOccurred())
			Expect(ac.Status.Phase).To(Equal("NotReady"))
			Expect(ac.Status.Conditions).To(ContainElement(
				HaveField("Reason", "SecretMissingField"),
			))
		})
	})

	Context("when the Secret is missing AWS_SECRET_ACCESS_KEY", func() {
		BeforeEach(func() {
			createAWSCreds()
			s := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Data:       map[string][]byte{"AWS_ACCESS_KEY_ID": []byte("AKIAIOSFODNN7EXAMPLE")},
			}
			Expect(k8sClient.Create(ctx, s)).To(Succeed())
		})

		It("sets phase=NotReady with SecretMissingField reason", func() {
			ac, err := reconcileAndGet()
			Expect(err).NotTo(HaveOccurred())
			Expect(ac.Status.Phase).To(Equal("NotReady"))
			Expect(ac.Status.Conditions).To(ContainElement(
				HaveField("Reason", "SecretMissingField"),
			))
		})
	})

	Context("when the Secret has empty credential values", func() {
		BeforeEach(func() {
			createAWSCreds()
			s := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Data: map[string][]byte{
					"AWS_ACCESS_KEY_ID":     []byte(""),
					"AWS_SECRET_ACCESS_KEY": []byte(""),
				},
			}
			Expect(k8sClient.Create(ctx, s)).To(Succeed())
		})

		It("sets phase=NotReady with SecretEmptyFields reason", func() {
			ac, err := reconcileAndGet()
			Expect(err).NotTo(HaveOccurred())
			Expect(ac.Status.Phase).To(Equal("NotReady"))
			Expect(ac.Status.Conditions).To(ContainElement(
				HaveField("Reason", "SecretEmptyFields"),
			))
		})
	})

	Context("when the Secret has both valid credential keys", func() {
		BeforeEach(func() {
			createAWSCreds()
			s := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Data: map[string][]byte{
					"AWS_ACCESS_KEY_ID":     []byte("AKIAIOSFODNN7EXAMPLE"),
					"AWS_SECRET_ACCESS_KEY": []byte("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
				},
			}
			Expect(k8sClient.Create(ctx, s)).To(Succeed())
		})

		It("sets phase=Ready and Ready=True with SecretFound reason", func() {
			ac, err := reconcileAndGet()
			Expect(err).NotTo(HaveOccurred())
			Expect(ac.Status.Phase).To(Equal("Ready"))
			Expect(ac.Status.Conditions).To(ContainElement(
				SatisfyAll(
					HaveField("Reason", "SecretFound"),
					HaveField("Status", metav1.ConditionTrue),
				),
			))
		})
	})

	Context("when the AWSCreds object does not exist", func() {
		It("returns no error and does nothing", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())
			ac := &corev1alpha1.AWSCreds{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "nonexistent", Namespace: namespace}, ac)
			Expect(k8serrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
