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
	"math"
	"os"

	"github.com/rs/zerolog/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kwasmv1 "github.com/kwasm/kwasm-operator/api/v1beta1"
	runtimev1beta1 "github.com/kwasm/kwasm-operator/api/v1beta1"
)

const (
	KwasmOperatorFinalizer      = "kwasm.sh/finalizer"
	addKWasmNodeLabelAnnotation = "kwasm.sh/kwasm-node"
	nodeNameLabel               = "kwasm.sh/kwasm-provisioned"
	AutoProvision               = true
)

// ShimReconciler reconciles a Shim object
type ShimReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

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
	log := log.With().Str("node", req.Name).Logger()
	node := &corev1.Node{}

	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get node: %w", err)
	}

	// 1. Check if the shim resource exists
	var shimResource kwasmv1.Shim
	if err := r.Client.Get(ctx, req.NamespacedName, &shimResource); err != nil {
		log.Err(err).Msg("Unable to fetch shimResource")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Spin app has been requested for deletion, delete the child resources
	if !shimResource.DeletionTimestamp.IsZero() {
		err := r.handleDeletion(ctx, &shimResource)
		if err != nil {
			return ctrl.Result{}, err
		}

		// tbd
		err = r.removeFinalizer(ctx, &shimResource)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Check which nodes the shim should be deployed on
	// determininglabel := r.Client.

	// 3. Add or remove the label.

	labelShouldBePresent := node.Annotations[addKWasmNodeLabelAnnotation] == "true"
	labelIsPresent := node.Labels[nodeNameLabel] == node.Name

	if labelShouldBePresent == labelIsPresent && !AutoProvision {
		// The desired state and actual state of the Node are the same.
		// No further action is required by the operator at this moment.

		return ctrl.Result{}, nil
	}
	if labelShouldBePresent || AutoProvision && !labelIsPresent {
		// If the label should be set but is not, set it.
		if node.Labels == nil {
			node.Labels = make(map[string]string)
		}
		node.Labels[nodeNameLabel] = node.Name
		log.Info().Msgf("Trying to Deploy on %s", node.Name)

		dep, err := r.deployJob(node, req)
		if err != nil {
			return ctrl.Result{}, err
		}

		if err = r.Create(ctx, dep); err != nil {
			log.Err(err).Msg("Failed to create new Job " + req.Namespace + " Job.Name " + req.Name)
			return ctrl.Result{}, fmt.Errorf("failed to create new job: %w", err)
		}
	} else if !AutoProvision {
		// If the label should not be set but is, remove it.
		delete(node.Labels, nodeNameLabel)
		log.Info().Msg("Label removed. Removing Job.")

		err := r.Delete(ctx, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name + "-provision-kwasm",
				Namespace: os.Getenv("CONTROLLER_NAMESPACE"),
			},
		}, client.PropagationPolicy(metav1.DeletePropagationBackground))
		if err != nil {
			log.Err(err).Msg("Failed to delete Job for " + req.Name)
		}
	}

	if err := r.Update(ctx, node); err != nil {
		if apierrors.IsConflict(err) {
			// The Node has been updated since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, nil
		}
		if apierrors.IsNotFound(err) {
			// The Node has been deleted since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error().Err(err).Msg("unable to update Node")
		return ctrl.Result{}, fmt.Errorf("failed to update Node: %w", err)
	}

	return ctrl.Result{}, nil
}

// handleDeletion deletes all possible child resources of a Shim. It will ignore NotFound errors.
func (r *ShimReconciler) handleDeletion(ctx context.Context, shim *kwasmv1.Shim) error {
	err := r.deleteShim(ctx, shim)
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	return nil
}

// deleteDeployment deletes the deployment for a Shim.
func (r *ShimReconciler) deleteShim(ctx context.Context, shim *kwasmv1.Shim) error {
	log.Info().Msgf("Deleting Shim... %s", shim.Name)

	s, err := r.findShim(ctx, shim)
	if err != nil {
		return err
	}

	err = r.Client.Delete(ctx, s)
	if err != nil {
		return err
	}

	log.Info().Msgf("Successfully deleted Shim... %s", shim.Name)

	return nil
}

func (r *ShimReconciler) findShim(ctx context.Context, shim *kwasmv1.Shim) (*kwasmv1.Shim, error) {
	var s kwasmv1.Shim
	err := r.Client.Get(ctx, types.NamespacedName{Name: shim.Name, Namespace: shim.Namespace}, &s)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// removeFinalizer removes the finalizer from a Shim.
func (r *ShimReconciler) removeFinalizer(ctx context.Context, shim *kwasmv1.Shim) error {
	if controllerutil.ContainsFinalizer(shim, KwasmOperatorFinalizer) {
		controllerutil.RemoveFinalizer(shim, KwasmOperatorFinalizer)
		if err := r.Client.Update(ctx, shim); err != nil {
			return err
		}
	}
	return nil
}

func (r *ShimReconciler) deployJob(node *corev1.Node, req ctrl.Request) (*batchv1.Job, error) {
	priv := true
	name := req.Name + "-provision-kwasm"
	nameMax := int(math.Min(float64(len(name)), 63))

	dep := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name[:nameMax],
			Namespace: os.Getenv("CONTROLLER_NAMESPACE"),
			Labels:    map[string]string{"kwasm.sh/job": "true"},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeName: req.Name,
					HostPID:  true,
					Volumes: []corev1.Volume{{
						Name: "root-mount",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/",
							},
						},
					}},
					Containers: []corev1.Container{{
						Image: "ghcr.io/kwasm/kwasm-node-installer:main",
						Name:  "kwasm-provision",
						SecurityContext: &corev1.SecurityContext{
							Privileged: &priv,
						},
						Env: []corev1.EnvVar{
							{
								Name:  "NODE_ROOT",
								Value: "/mnt/node-root",
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "root-mount",
								MountPath: "/mnt/node-root",
							},
						},
					}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(node, dep, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	return dep, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ShimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1beta1.Shim{}).
		Complete(r)
}
