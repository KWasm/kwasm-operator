/*
Copyright 2024.

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

	"github.com/rs/zerolog/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// JobReconciler reconciles a Job object
type JobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=jobs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=jobs/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (jr *JobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&batchv1.Job{}).
		Complete(jr)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Job object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (jr *JobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.With().Str("job", req.Name).Logger()
	log.Debug().Msg("Job Reconciliation started!")

	job := &batchv1.Job{}

	if err := jr.Get(ctx, req.NamespacedName, job); err != nil {
		if apierrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			return ctrl.Result{}, nil
		}
		log.Error().Msgf("Unable to get Job: %s", err)
		return ctrl.Result{}, fmt.Errorf("failed to get Job: %w", err)
	}

	if _, exists := job.Labels["kwasm.sh/shimName"]; !exists {
		return ctrl.Result{}, nil
	}

	shimName := job.Labels["kwasm.sh/shimName"]

	node, err := jr.getNode(ctx, job.Spec.Template.Spec.NodeName, req)
	if err != nil {
		return ctrl.Result{}, nil
	}

	_, finishedType := jr.isJobFinished(job)
	switch finishedType {
	case "": // ongoing
		log.Info().Msgf("Job %s is still Ongoing", job.Name)
		// if err := jr.updateNodeLabels(ctx, node, shimName, "pending", req); err != nil {
		// 	log.Error().Msgf("Unable to update node label %s: %s", shimName, err)
		// }
		return ctrl.Result{}, nil
	case batchv1.JobFailed:
		log.Info().Msgf("Job %s is still failing...", job.Name)
		if err := jr.updateNodeLabels(ctx, node, shimName, "failed", req); err != nil {
			log.Error().Msgf("Unable to update node label %s: %s", shimName, err)
		}
		return ctrl.Result{}, nil
	case batchv1.JobFailureTarget:
		log.Info().Msgf("Job %s is about to fail", job.Name)
		if err := jr.updateNodeLabels(ctx, node, shimName, "failed", req); err != nil {
			log.Error().Msgf("Unable to update node label %s: %s", shimName, err)
		}
		return ctrl.Result{}, nil
	case batchv1.JobComplete:
		log.Info().Msgf("Job %s is Completed. Happy WASMing", job.Name)
		if err := jr.updateNodeLabels(ctx, node, shimName, "provisioned", req); err != nil {
			log.Error().Msgf("Unable to update node label %s: %s", shimName, err)
		}
		return ctrl.Result{}, nil
	case batchv1.JobSuspended:
		log.Info().Msgf("Job %s is suspended", job.Name)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (jr *JobReconciler) updateNodeLabels(ctx context.Context, node *corev1.Node, shimName string, status string, req ctrl.Request) error {
	node.Labels[shimName] = status

	if err := jr.Update(ctx, node); err != nil {
		return err
	}

	return nil
}

func (jr *JobReconciler) getNode(ctx context.Context, nodeName string, req ctrl.Request) (*corev1.Node, error) {
	node := corev1.Node{}
	if err := jr.Client.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		log.Err(err).Msg("Unable to fetch node")
		return &corev1.Node{}, client.IgnoreNotFound(err)
	}
	return &node, nil
}

func (jr *JobReconciler) isJobFinished(job *batchv1.Job) (bool, batchv1.JobConditionType) {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true, c.Type
		}
	}

	return false, ""
}
