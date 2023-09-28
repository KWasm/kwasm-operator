/*
Copyright 2023.

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

package controllers

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kwasm/kwasm-operator/api/v1beta1"
	runtimev1beta1 "github.com/kwasm/kwasm-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ShimReconciler reconciles a Shim object
type ShimReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Definitions to manage status conditions
const (
	// typeAvailableShim represents the status of the Deployment reconciliation
	typeAvailableShim = "Available"
	// typeDegradedShim represents the status used when the custom resource is deleted and the finalizer operations are must to occur.
	typeDegradedShim = "Degraded"
)

//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Shim object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *ShimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	// Fetch the Shim custom resource
	shim := &v1beta1.Shim{}
	err := r.Get(ctx, req.NamespacedName, shim)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Create a new Kubernetes Job based on the Shim custom resource
	job := r.buildJobForShim(shim)

	// Set the owner reference to the Shim custom resource
	if err := controllerutil.SetControllerReference(shim, job, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Check if the Job already exists, if not, create it
	found := &batchv1.Job{}
	err = r.Get(ctx, req.NamespacedName, found)
	if err != nil && client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	if err != nil {
		// Job does not exist, create it
		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Job already exists, do nothing
	return ctrl.Result{}, nil
}

func (r *ShimReconciler) buildJobForShim(shim *v1beta1.Shim) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("shim-job-%s", shim.Name),
			Namespace: os.Getenv("CONTROLLER_NAMESPACE"),
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "my-shim-container",
							Image: "your-specific-image", // Set your specific image here
							// Add other container settings as needed
						},
					},
					RestartPolicy: "Never",
				},
			},
		},
	}
	return job
}

// SetupWithManager sets up the controller with the Manager.
func (r *ShimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1beta1.Shim{}).
		Complete(r)
}
