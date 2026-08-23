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

package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ec2v1alpha1 "github.com/aditya130103/infra-operator/api/ec2/v1alpha1"
	corev1alpha1 "github.com/aditya130103/infra-operator/api/v1alpha1"
)

// fakeEC2 is a controllable stub that satisfies EC2API.
type fakeEC2 struct {
	createOut   *awsec2.CreateVpcOutput
	createErr   error
	describeOut *awsec2.DescribeVpcsOutput
	describeErr error
	deleteErr   error
}

func (f *fakeEC2) CreateVpc(_ context.Context, _ *awsec2.CreateVpcInput, _ ...func(*awsec2.Options)) (*awsec2.CreateVpcOutput, error) {
	return f.createOut, f.createErr
}
func (f *fakeEC2) DescribeVpcs(_ context.Context, _ *awsec2.DescribeVpcsInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
	return f.describeOut, f.describeErr
}
func (f *fakeEC2) DeleteVpc(_ context.Context, _ *awsec2.DeleteVpcInput, _ ...func(*awsec2.Options)) (*awsec2.DeleteVpcOutput, error) {
	return &awsec2.DeleteVpcOutput{}, f.deleteErr
}

// notFoundAPIError returns a smithy.APIError whose code is InvalidVpcID.NotFound,
// matching what isVPCNotFound() checks.
func notFoundAPIError() error {
	return &smithy.GenericAPIError{Code: "InvalidVpcID.NotFound", Message: "does not exist"}
}

var _ = Describe("VPC Controller", func() {
	const (
		namespace   = "default"
		vpcName     = "test-vpc"
		credsName   = "test-awscreds"
		secretName  = "test-aws-secret"
		vpcCIDR     = "10.0.0.0/16"
		vpcConsName = "my-test-vpc"
		region      = "ap-south-2"
		phaseReady  = "Ready"
		vpcToDelete = "vpc-todelete"
	)

	ctx := context.Background()
	namespacedName := types.NamespacedName{Name: vpcName, Namespace: namespace}

	// helpers ----------------------------------------------------------------

	createSecret := func() {
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			Data: map[string][]byte{
				"AWS_ACCESS_KEY_ID":     []byte("AKIAIOSFODNN7EXAMPLE"),
				"AWS_SECRET_ACCESS_KEY": []byte("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
			},
		}
		Expect(k8sClient.Create(ctx, s)).To(Succeed())
	}

	createReadyAWSCreds := func() {
		ac := &corev1alpha1.AWSCreds{
			ObjectMeta: metav1.ObjectMeta{Name: credsName, Namespace: namespace},
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
		// drive the status subresource directly — envtest respects status updates
		ac.Status.Phase = phaseReady
		Expect(k8sClient.Status().Update(ctx, ac)).To(Succeed())
	}

	createVPC := func() {
		v := &ec2v1alpha1.VPC{
			ObjectMeta: metav1.ObjectMeta{Name: vpcName, Namespace: namespace},
			Spec: ec2v1alpha1.VPCSpec{
				Name:      vpcConsName,
				CIDRBlock: vpcCIDR,
				Region:    region,
				AWSCredsRef: ec2v1alpha1.AWSCredsRefSpec{
					Name:      credsName,
					Namespace: namespace,
				},
			},
		}
		Expect(k8sClient.Create(ctx, v)).To(Succeed())
	}

	newReconciler := func(fake *fakeEC2) *VPCReconciler {
		r := &VPCReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		if fake != nil {
			r.NewEC2Client = func(_ context.Context, _ *corev1.Secret, _ string) (EC2API, error) {
				return fake, nil
			}
		}
		return r
	}

	reconcile1 := func(r *VPCReconciler) *ec2v1alpha1.VPC {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_ = err // some cases return an error intentionally; callers assert on status
		v := &ec2v1alpha1.VPC{}
		Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
		return v
	}

	cleanup := func() {
		v := &ec2v1alpha1.VPC{}
		if err := k8sClient.Get(ctx, namespacedName, v); err == nil {
			// strip finalizer so Delete doesn't block in envtest
			v.Finalizers = nil
			_ = k8sClient.Update(ctx, v)
			_ = k8sClient.Delete(ctx, v)
		}
		ac := &corev1alpha1.AWSCreds{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: credsName, Namespace: namespace}, ac); err == nil {
			_ = k8sClient.Delete(ctx, ac)
		}
		s := &corev1.Secret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, s); err == nil {
			_ = k8sClient.Delete(ctx, s)
		}
	}

	AfterEach(cleanup)

	// cases ------------------------------------------------------------------

	Context("step 3 — first reconcile on a new VPC", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("adds the finalizer and re-queues", func() {
			r := newReconciler(nil)
			// first reconcile: no finalizer yet → adds it, returns immediately
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			Expect(v.Finalizers).To(ContainElement(vpcFinalizer))
		})
	})

	Context("step 4 — AWSCreds not found", func() {
		BeforeEach(func() { createVPC() })

		It("sets phase=Pending and reason=AWSCredsNotReady", func() {
			r := newReconciler(nil)
			// first reconcile adds finalizer
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// second reconcile: fetchEC2Client fails (no AWSCreds object)
			v := reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Pending"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "AWSCredsNotReady"),
			))
		})
	})

	Context("step 4 — AWSCreds exists but phase is NotReady", func() {
		BeforeEach(func() {
			ac := &corev1alpha1.AWSCreds{
				ObjectMeta: metav1.ObjectMeta{Name: credsName, Namespace: namespace},
				Spec: corev1alpha1.AWSCredsSpec{
					Credentials: corev1alpha1.CredentialsSpec{
						SecretRef: corev1alpha1.SecretRefSpec{Name: secretName, Namespace: namespace},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ac)).To(Succeed())
			ac.Status.Phase = "NotReady"
			Expect(k8sClient.Status().Update(ctx, ac)).To(Succeed())
			createVPC()
		})

		It("sets phase=Pending and reason=AWSCredsNotReady", func() {
			r := newReconciler(nil)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer
			v := reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Pending"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "AWSCredsNotReady"),
			))
		})
	})

	Context("step 6 — CreateVpc succeeds", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("stores the VpcID and sets phase=Creating", func() {
			fake := &fakeEC2{
				createOut: &awsec2.CreateVpcOutput{
					Vpc: &awsec2types.Vpc{VpcId: aws.String("vpc-abc123")},
				},
			}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer
			v := reconcile1(r)                                                         // fetch creds + CreateVpc
			Expect(v.Status.VpcID).To(Equal("vpc-abc123"))
			Expect(v.Status.Phase).To(Equal("Creating"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "VPCCreating"),
			))
		})
	})

	Context("step 5 — DescribeVpcs returns available", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("sets phase=Available and Ready=True", func() {
			fake := &fakeEC2{
				describeOut: &awsec2.DescribeVpcsOutput{
					Vpcs: []awsec2types.Vpc{
						{VpcId: aws.String("vpc-abc123"), State: awsec2types.VpcStateAvailable},
					},
				},
			}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

			// manually set VpcID to trigger observe path
			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			v.Status.VpcID = "vpc-abc123"
			Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

			v = reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Available"))
			Expect(v.Status.Conditions).To(ContainElement(
				SatisfyAll(
					HaveField("Reason", "VPCAvailable"),
					HaveField("Status", metav1.ConditionTrue),
				),
			))
		})
	})

	Context("step 5 — drift: DescribeVpcs returns InvalidVpcID.NotFound", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("clears VpcID and sets phase=Pending with VPCNotFound reason", func() {
			fake := &fakeEC2{describeErr: notFoundAPIError()}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			v.Status.VpcID = "vpc-gone123"
			Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

			v = reconcile1(r)
			Expect(v.Status.VpcID).To(BeEmpty())
			Expect(v.Status.Phase).To(Equal("Pending"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "VPCNotFound"),
			))
		})
	})

	Context("step 2 — deletion path", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("calls DeleteVpc and removes the finalizer", func() {
			fake := &fakeEC2{deleteErr: nil}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

			// set VpcID so deletion path calls DeleteVpc
			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			v.Status.VpcID = vpcToDelete
			Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

			// trigger deletion
			Expect(k8sClient.Delete(ctx, v)).To(Succeed())

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// object should be gone (finalizer removed → GC completed)
			gone := &ec2v1alpha1.VPC{}
			err = k8sClient.Get(ctx, namespacedName, gone)
			Expect(err).To(HaveOccurred())
			Expect(gone.Finalizers).NotTo(ContainElement(vpcFinalizer))
		})
	})

	Context("step 6 — CreateVpc fails", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("sets phase=Failed and reason=CreateFailed", func() {
			fake := &fakeEC2{createErr: fmt.Errorf("AWS error: VpcLimitExceeded")}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer
			v := reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Failed"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "CreateFailed"),
			))
		})
	})

	Context("step 5 — DescribeVpcs returns pending (VPC still provisioning)", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("sets phase=Creating and Ready=False", func() {
			fake := &fakeEC2{
				describeOut: &awsec2.DescribeVpcsOutput{
					Vpcs: []awsec2types.Vpc{
						{VpcId: aws.String("vpc-abc123"), State: awsec2types.VpcStatePending},
					},
				},
			}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

			// manually set VpcID to trigger observe path instead of create path
			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			v.Status.VpcID = "vpc-abc123"
			Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

			v = reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Creating"))
			Expect(v.Status.Conditions).To(ContainElement(
				SatisfyAll(
					HaveField("Reason", "VPCNotReady"),
					HaveField("Status", metav1.ConditionFalse),
				),
			))
		})
	})

	Context("step 2 — deletion: credentials not ready", func() {
		BeforeEach(func() {
			createVPC() // no AWSCreds, no Secret — creds fetch will fail
		})

		It("sets phase=Deleting and reason=AWSCredsNotReady, keeps finalizer", func() {
			r := newReconciler(nil)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			v.Status.VpcID = vpcToDelete
			Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

			Expect(k8sClient.Delete(ctx, v)).To(Succeed())

			v = reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Deleting"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "AWSCredsNotReady"),
			))
			Expect(v.Finalizers).To(ContainElement(vpcFinalizer))
		})
	})

	Context("step 2 — deletion: DeleteVpc fails", func() {
		BeforeEach(func() {
			createSecret()
			createReadyAWSCreds()
			createVPC()
		})

		It("sets phase=Deleting and reason=DeleteFailed, keeps finalizer", func() {
			fake := &fakeEC2{deleteErr: fmt.Errorf("DependencyViolation: has dependencies and cannot be deleted")}
			r := newReconciler(fake)
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

			v := &ec2v1alpha1.VPC{}
			Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
			v.Status.VpcID = vpcToDelete
			Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

			Expect(k8sClient.Delete(ctx, v)).To(Succeed())

			v = reconcile1(r)
			Expect(v.Status.Phase).To(Equal("Deleting"))
			Expect(v.Status.Conditions).To(ContainElement(
				HaveField("Reason", "DeleteFailed"),
			))
			Expect(v.Finalizers).To(ContainElement(vpcFinalizer))
		})
	})
})
