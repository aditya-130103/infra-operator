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

	corev1alpha1 "github.com/aditya130103/infra-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	handler "sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	reconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AWSCredsReconciler reconciles a AWSCreds object
type AWSCredsReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	phaseReady    = "Ready"
	phaseNotReady = "NotReady"
	phaseNotFound = "NotFound"
)

// +kubebuilder:rbac:groups=core.infra.example.com,resources=awscreds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.infra.example.com,resources=awscreds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.infra.example.com,resources=awscreds/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AWSCreds object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *AWSCredsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.Info("Reconciling AWSCreds")
	// step 1: fetch the aws creds
	awsCreds := &corev1alpha1.AWSCreds{}
	if err := r.Get(ctx, req.NamespacedName, awsCreds); err != nil {
		if errors.IsNotFound(err) {
			log.Info("AWSCreds not found, returning")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Error fetching AWSCreds")
		return ctrl.Result{}, err
	}

	// step 2: check if the secret mentioned in the aws creds exists
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: awsCreds.Spec.Credentials.SecretRef.Namespace, Name: awsCreds.Spec.Credentials.SecretRef.Name}, secret); err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Secret not found, returning")
			awsCreds.Status.Phase = phaseNotFound

			meta.SetStatusCondition(&awsCreds.Status.Conditions, metav1.Condition{
				Type:    phaseReady,
				Status:  metav1.ConditionFalse,
				Reason:  "SecretNotFound",
				Message: "Secret not found",
			})
			if err := r.Status().Update(ctx, awsCreds); err != nil {
				log.Error(err, "Error updating AWSCreds status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		log.Error(err, "Error fetching Secret")
		return ctrl.Result{}, err
	}

	// step 3: Check if the secret has the aws access key id feild
	var awsAccessKeyID, awsSecretAccessKey []byte
	var ok bool
	if awsAccessKeyID, ok = secret.Data["AWS_ACCESS_KEY_ID"]; !ok {
		log.Info("Secret missing AWS_ACCESS_KEY_ID", "Secret name", secret.Name)
		awsCreds.Status.Phase = phaseNotReady
		meta.SetStatusCondition(&awsCreds.Status.Conditions, metav1.Condition{
			Type:    phaseReady,
			Status:  metav1.ConditionFalse,
			Reason:  "SecretMissingField",
			Message: "Secret missing AWS_ACCESS_KEY_ID",
		})
		if err := r.Status().Update(ctx, awsCreds); err != nil {
			log.Error(err, "Error updating AWSCreds status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// step 4: Check if the secret has the aws secret access key feild
	if awsSecretAccessKey, ok = secret.Data["AWS_SECRET_ACCESS_KEY"]; !ok {
		log.Info("Secret missing AWS_SECRET_ACCESS_KEY", "Secret name", secret.Name)
		awsCreds.Status.Phase = phaseNotReady
		meta.SetStatusCondition(&awsCreds.Status.Conditions, metav1.Condition{
			Type:    phaseReady,
			Status:  metav1.ConditionFalse,
			Reason:  "SecretMissingField",
			Message: "Secret missing AWS_SECRET_ACCESS_KEY",
		})
		if err := r.Status().Update(ctx, awsCreds); err != nil {
			log.Error(err, "Error updating AWSCreds status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// step 5: Make sure that both the feilds are not empty
	if len(awsAccessKeyID) == 0 || len(awsSecretAccessKey) == 0 {
		log.Info("Secret has empty fields", "Secret name", secret.Name)
		awsCreds.Status.Phase = phaseNotReady
		meta.SetStatusCondition(&awsCreds.Status.Conditions, metav1.Condition{
			Type:    phaseReady,
			Status:  metav1.ConditionFalse,
			Reason:  "SecretEmptyFields",
			Message: "Secret has empty AWS_ACCESS_KEY_ID or AWS_SECRET_ACCESS_KEY",
		})
		if err := r.Status().Update(ctx, awsCreds); err != nil {
			log.Error(err, "Error updating AWSCreds status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// step 6: Update the status of the AWSCreds
	awsCreds.Status.Phase = phaseReady
	meta.SetStatusCondition(&awsCreds.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "SecretFound",
		Message: "Secret found and has valid AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY",
	})
	if err := r.Status().Update(ctx, awsCreds); err != nil {
		log.Error(err, "Error updating AWSCreds status")
		return ctrl.Result{}, err
	}
	log.Info("AWSCreds reconciled successfully")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AWSCredsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.AWSCreds{}).
		// watch for changes in the secret and enqueue the request for the awscreds that reference the secret
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			// fetch all the awscreds that reference the secret
			awscreds := &corev1alpha1.AWSCredsList{}
			if err := r.List(ctx, awscreds); err != nil {
				return nil
			}
			var requests []reconcile.Request
			for _, awscred := range awscreds.Items {
				if awscred.Spec.Credentials.SecretRef.Namespace == obj.GetNamespace() && awscred.Spec.Credentials.SecretRef.Name == obj.GetName() {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: awscred.Namespace,
							Name:      awscred.Name,
						},
					})
				}
			}
			return requests
		},
		)).
		Named("awscreds").
		Complete(r)
}
