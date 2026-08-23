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
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ec2v1alpha1 "github.com/aditya130103/infra-operator/api/ec2/v1alpha1"
	corev1alpha1 "github.com/aditya130103/infra-operator/api/v1alpha1"
)

const vpcFinalizer = "vpc.ec2.infra.example.com/finalizer"
const phaseReady = "Ready"

// EC2API covers the EC2 operations used by VPCReconciler, allowing tests to inject a fake.
type EC2API interface {
	CreateVpc(ctx context.Context, params *awsec2.CreateVpcInput, optFns ...func(*awsec2.Options)) (*awsec2.CreateVpcOutput, error)
	DescribeVpcs(ctx context.Context, params *awsec2.DescribeVpcsInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error)
	DeleteVpc(ctx context.Context, params *awsec2.DeleteVpcInput, optFns ...func(*awsec2.Options)) (*awsec2.DeleteVpcOutput, error)
}

// buildEC2Client builds an AWS EC2 client from a Secret's credential data and a region.
func buildEC2Client(ctx context.Context, secret *corev1.Secret, region string) (*awsec2.Client, error) {
	accessKeyID := string(secret.Data["AWS_ACCESS_KEY_ID"])
	secretAccessKey := string(secret.Data["AWS_SECRET_ACCESS_KEY"])

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, err
	}
	return awsec2.NewFromConfig(cfg), nil
}

// VPCReconciler reconciles a VPC object
type VPCReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// NewEC2Client overrides EC2 client construction. When nil, buildEC2Client is used.
	// Tests set this to inject a fake; production leaves it nil.
	NewEC2Client func(ctx context.Context, secret *corev1.Secret, region string) (EC2API, error)
}

// helper functions - to check if a string is in a slice and to remove a string from a slice
func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// isVPCNotFound checks whether an AWS API error is the InvalidVpcID.NotFound error code.
//
// When the AWS SDK returns an error, it wraps it in a smithy.APIError — a structured
// object with a machine-readable ErrorCode() field (e.g. "InvalidVpcID.NotFound") and a
// human-readable ErrorMessage() field (e.g. "The vpc ID 'vpc-xxx' does not exist").
//
//	errors.As(err, &apiErr) + apiErr.ErrorCode() == "InvalidVpcID.NotFound":
//	ErrorCode() reads directly from the structured field that AWS explicitly guarantees
//	as part of their API contract. It will not change when the human-readable message
//	changes. This is the same approach used by the AWS SDK's own service-specific
//	error helpers (e.g. types.IsNotFoundException).
func isVPCNotFound(err error) bool {
	// errors.As walks the error chain looking for a value that implements smithy.APIError.
	// The AWS SDK wraps all service errors in this interface, so this always succeeds for
	// AWS API errors regardless of how many layers of wrapping exist.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "InvalidVpcID.NotFound"
	}
	return false
}

// +kubebuilder:rbac:groups=ec2.infra.example.com,resources=vpcs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ec2.infra.example.com,resources=vpcs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ec2.infra.example.com,resources=vpcs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;
// +kubebuilder:rbac:groups=core.infra.example.com,resources=awscreds,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VPC object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *VPCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// step 1: fetch the vpc
	vpc := &ec2v1alpha1.VPC{}
	if err := r.Get(ctx, req.NamespacedName, vpc); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("VPC not found, returning")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Error fetching VPC")
		return ctrl.Result{}, err
	}
	// step 2: check deletion path first — requires its own creds fetch
	if vpc.DeletionTimestamp != nil {
		// if finalizer is not present, cleanup already done
		if !containsString(vpc.Finalizers, vpcFinalizer) {
			return ctrl.Result{}, nil
		}

		// fetch creds specifically for deletion
		ec2Client, err := r.fetchEC2Client(ctx, vpc)
		if err != nil {
			log.Error(err, "Failed to build EC2 client for deletion")
			vpc.Status.Phase = "Deleting"
			meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
				Type:    phaseReady,
				Status:  metav1.ConditionFalse,
				Reason:  "AWSCredsNotReady",
				Message: fmt.Sprintf("Cannot delete VPC — credentials not ready: %s", err.Error()),
			})
			if statusErr := r.Status().Update(ctx, vpc); statusErr != nil {
				log.Error(statusErr, "Failed to update VPC status after creds failure during deletion")
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// only call DeleteVpc if we actually created one
		if vpc.Status.VpcID != "" {
			_, err = ec2Client.DeleteVpc(ctx, &awsec2.DeleteVpcInput{
				VpcId: aws.String(vpc.Status.VpcID),
			})
			if err != nil && !isVPCNotFound(err) {
				// real AWS error — retry with backoff
				log.Error(err, "Failed to delete VPC in AWS", "vpcId", vpc.Status.VpcID)
				vpc.Status.Phase = "Deleting"
				meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
					Type:    phaseReady,
					Status:  metav1.ConditionFalse,
					Reason:  "DeleteFailed",
					Message: fmt.Sprintf("AWS DeleteVpc failed: %s", err.Error()),
				})
				if statusErr := r.Status().Update(ctx, vpc); statusErr != nil {
					log.Error(statusErr, "Failed to update VPC status after delete failure")
				}
				return ctrl.Result{}, err
			}
			// err == nil OR InvalidVpcID.NotFound — either way VPC is gone
			log.Info("VPC deleted or already gone in AWS", "vpcId", vpc.Status.VpcID)
		}

		// remove finalizer so K8s garbage collects the object
		vpc.Finalizers = removeString(vpc.Finalizers, vpcFinalizer)
		if err := r.Update(ctx, vpc); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Step 3: Check if finalizer is present
	if !containsString(vpc.Finalizers, vpcFinalizer) {
		vpc.Finalizers = append(vpc.Finalizers, vpcFinalizer)
		if err := r.Update(ctx, vpc); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Step 4: Fetch the credentials secret and build an EC2 client
	ec2Client, err := r.fetchEC2Client(ctx, vpc)
	if err != nil {
		log.Error(err, "Failed to build EC2 client")
		vpc.Status.Phase = "Pending"
		meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
			Type:    phaseReady,
			Status:  metav1.ConditionFalse,
			Reason:  "AWSCredsNotReady",
			Message: err.Error(),
		})
		if statusErr := r.Status().Update(ctx, vpc); statusErr != nil {
			log.Error(statusErr, "Failed to update VPC status after creds failure")
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Step 5: Observe — VpcID already set, check AWS state (drift detection)
	if vpc.Status.VpcID != "" {
		result, err := ec2Client.DescribeVpcs(ctx, &awsec2.DescribeVpcsInput{
			VpcIds: []string{vpc.Status.VpcID},
		})
		if err != nil {
			if isVPCNotFound(err) {
				// DescribeVpcs with a specific VPC ID returns a 400 InvalidVpcID.NotFound
				// error (not an empty list) when the VPC doesn't exist. This is drift —
				// the VPC was deleted outside of the operator (e.g. AWS console or CLI).
				// Clear the VpcID so the next reconcile re-creates it.
				log.Info("VPC not found in AWS (drift detected) — will re-create", "vpcId", vpc.Status.VpcID)
				vpc.Status.VpcID = ""
				vpc.Status.Phase = "Pending"
				meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
					Type:    phaseReady,
					Status:  metav1.ConditionFalse,
					Reason:  "VPCNotFound",
					Message: "VPC not found in AWS, re-creating",
				})
				if err := r.Status().Update(ctx, vpc); err != nil {
					log.Error(err, "Failed to update VPC status after drift")
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			// real AWS error — retry with backoff
			log.Error(err, "Failed to describe VPC", "vpcId", vpc.Status.VpcID)
			vpc.Status.Phase = "Failed"
			meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
				Type:    phaseReady,
				Status:  metav1.ConditionFalse,
				Reason:  "DescribeFailed",
				Message: fmt.Sprintf("AWS DescribeVpcs failed: %s", err.Error()),
			})
			if statusErr := r.Status().Update(ctx, vpc); statusErr != nil {
				log.Error(statusErr, "Failed to update VPC status after describe failure")
			}
			return ctrl.Result{}, err
		}

		// VPC exists — update phase based on AWS state
		awsState := string(result.Vpcs[0].State)
		if awsState == "available" {
			vpc.Status.Phase = "Available"
			meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
				Type:    phaseReady,
				Status:  metav1.ConditionTrue,
				Reason:  "VPCAvailable",
				Message: "VPC is available in AWS",
			})
		} else {
			vpc.Status.Phase = "Creating"
			meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
				Type:    phaseReady,
				Status:  metav1.ConditionFalse,
				Reason:  "VPCNotReady",
				Message: fmt.Sprintf("VPC state is %s", awsState),
			})
		}
		if err := r.Status().Update(ctx, vpc); err != nil {
			log.Error(err, "Failed to update VPC status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Step 6: Create — VpcID empty, VPC not yet created in AWS
	createResult, err := ec2Client.CreateVpc(ctx, &awsec2.CreateVpcInput{
		CidrBlock: aws.String(vpc.Spec.CIDRBlock),
		TagSpecifications: []awsec2types.TagSpecification{
			{
				ResourceType: awsec2types.ResourceTypeVpc,
				Tags: []awsec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(vpc.Spec.Name)},
				},
			},
		},
	})
	if err != nil {
		log.Error(err, "Failed to create VPC in AWS")
		vpc.Status.Phase = "Failed"
		meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
			Type:    phaseReady,
			Status:  metav1.ConditionFalse,
			Reason:  "CreateFailed",
			Message: err.Error(),
		})
		if statusErr := r.Status().Update(ctx, vpc); statusErr != nil {
			log.Error(statusErr, "Failed to update VPC status after create failure")
		}
		return ctrl.Result{}, err
	}

	vpc.Status.VpcID = aws.ToString(createResult.Vpc.VpcId)
	vpc.Status.Phase = "Creating"
	meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "VPCCreating",
		Message: fmt.Sprintf("VPC %s is being created", vpc.Status.VpcID),
	})
	if err := r.Status().Update(ctx, vpc); err != nil {
		log.Error(err, "Failed to update VPC status after create")
		return ctrl.Result{}, err
	}
	log.Info("Created VPC in AWS", "vpcId", vpc.Status.VpcID)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// fetchEC2Client fetches AWSCreds + Secret for this VPC and builds an EC2 client.
func (r *VPCReconciler) fetchEC2Client(ctx context.Context, vpc *ec2v1alpha1.VPC) (EC2API, error) {
	log := logf.FromContext(ctx)

	awsCreds := &corev1alpha1.AWSCreds{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: vpc.Spec.AWSCredsRef.Namespace,
		Name:      vpc.Spec.AWSCredsRef.Name,
	}, awsCreds); err != nil {
		log.Error(err, "Failed to fetch AWSCreds")
		return nil, err
	}

	if awsCreds.Status.Phase != phaseReady {
		return nil, fmt.Errorf("AWSCreds %s/%s is not Ready", awsCreds.Namespace, awsCreds.Name)
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: awsCreds.Spec.Credentials.SecretRef.Namespace,
		Name:      awsCreds.Spec.Credentials.SecretRef.Name,
	}, secret); err != nil {
		log.Error(err, "Failed to fetch credentials Secret")
		return nil, err
	}

	if r.NewEC2Client != nil {
		return r.NewEC2Client(ctx, secret, vpc.Spec.Region)
	}
	return buildEC2Client(ctx, secret, vpc.Spec.Region)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VPCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ec2v1alpha1.VPC{}).
		Named("ec2-vpc").
		Complete(r)
}
