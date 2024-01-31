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
	"math"
	"os"

	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kwasmv1 "github.com/kwasm/kwasm-operator/api/v1alpha1"
)

const (
	KwasmOperatorFinalizer      = "kwasm.sh/finalizer"
	addKWasmNodeLabelAnnotation = "kwasm.sh/"
	nodeNameLabel               = "kwasm.sh/"
)

// ShimReconciler reconciles a Shim object
type ShimReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	AutoProvision bool
}

//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (sr *ShimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kwasmv1.Shim{}).
		Owns(&batchv1.Job{}).
		Complete(sr)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Shim object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (sr *ShimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.With().Str("shim", req.Name).Logger()
	log.Debug().Msg("Reconciliation started!")

	// 1. Check if the shim resource exists
	var shimResource kwasmv1.Shim
	if err := sr.Client.Get(ctx, req.NamespacedName, &shimResource); err != nil {
		log.Err(err).Msg("Unable to fetch shimResource")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Shim has been requested for deletion, delete the child resources
	if !shimResource.DeletionTimestamp.IsZero() {
		log.Debug().Msg("deletion started!")
		err := sr.handleDeletion(ctx, &shimResource)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Debug().Msg("removing finalizer!")
		err = sr.removeFinalizer(ctx, &shimResource)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Check if referenced runtimeClass exists in cluster
	rcExists, err := sr.runtimeClassExists(ctx, &shimResource)
	if err != nil {
		log.Error().Msgf("RuntimeClass issue: %s", err)
	}
	if !rcExists {
		log.Info().Msgf("RuntimeClass '%s' not found", shimResource.Spec.RuntimeClass.Name)
		_, err = sr.handleDeployRuntmeClass(ctx, &shimResource)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// 3. Get list of nodes
	nodes := &corev1.NodeList{}
	if shimResource.Spec.NodeSelector != nil {
		// 3.1 that match the nodeSelector
		err = sr.List(ctx, nodes, client.InNamespace(req.Namespace), client.MatchingLabels(shimResource.Spec.NodeSelector))
	} else {
		// 3.2 or no selector at all (all nodes)
		err = sr.List(ctx, nodes, client.InNamespace(req.Namespace))
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	// 4. Deploy job to each node in list
	if len(nodes.Items) != 0 {
		_, err = sr.handleDeployJob(ctx, &shimResource, nodes, req)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		log.Info().Msg("No nodes found")
	}

	return ctrl.Result{}, nil
}

// handleDeployJob deploys a Job to each node in a list.
func (sr *ShimReconciler) handleDeployJob(ctx context.Context, shim *kwasmv1.Shim, nodes *corev1.NodeList, req ctrl.Request) (ctrl.Result, error) {
	switch shim.Spec.RolloutStrategy.Type {
	case "rolling":
		{
			log.Debug().Msgf("Rolling strategy selected: maxUpdate=%d", shim.Spec.RolloutStrategy.Rolling.MaxUpdate)
		}
	case "recreate":
		{
			log.Debug().Msgf("Recreate strategy selected")
			for i := range nodes.Items {
				node := nodes.Items[i]
				log.Info().Msgf("Deploying on node: %s", node.Name)
				job, err := sr.createJobManifest(shim, &node, req)
				if err != nil {
					return ctrl.Result{}, err
				}

				// We want to use server-side apply https://kubernetes.io/docs/reference/using-api/server-side-apply
				patchMethod := client.Apply
				patchOptions := &client.PatchOptions{
					Force:        ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
					FieldManager: "shim-operator",
				}

				// Note that we reconcile even if the deployment is in a good state. We rely on controller-runtime to rate limit us.
				if err := sr.Client.Patch(ctx, job, patchMethod, patchOptions); err != nil {
					log.Error().Msgf("Unable to reconcile Job %s", err)
					return ctrl.Result{}, err
				}
			}
		}
	default:
		{
			log.Debug().Msgf("No rollout strategy selected; using default: rolling")
		}
	}

	return ctrl.Result{}, nil
}

// createJobManifest creates a Job manifest for a Shim.
func (sr *ShimReconciler) createJobManifest(shim *kwasmv1.Shim, node *corev1.Node, req ctrl.Request) (*batchv1.Job, error) {
	priv := true
	name := node.Name + "." + shim.Name
	nameMax := int(math.Min(float64(len(name)), 63))

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Job",
			APIVersion: "batch/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name[:nameMax],
			Namespace: os.Getenv("CONTROLLER_NAMESPACE"),
			Labels:    map[string]string{name[:nameMax]: "true"},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeName: node.Name,
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
						Image: "voigt/kwasm-node-installer:new",
						Name:  "provisioner",
						SecurityContext: &corev1.SecurityContext{
							Privileged: &priv,
						},
						Env: []corev1.EnvVar{
							{
								Name:  "NODE_ROOT",
								Value: "/mnt/node-root",
							},
							{
								Name:  "SHIM_LOCATION",
								Value: shim.Spec.FetchStrategy.AnonHttp.Location,
							},
							{
								Name:  "RUNTIMECLASS_NAME",
								Value: shim.Spec.RuntimeClass.Name,
							},
							{
								Name:  "RUNTIMECLASS_HANDLER",
								Value: shim.Spec.RuntimeClass.Handler,
							},
							{
								Name:  "SHIM_FETCH_STRATEGY",
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
	if err := ctrl.SetControllerReference(shim, job, sr.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	return job, nil
}

// handleDeployRuntmeClass deploys a RuntimeClass for a Shim.
func (sr *ShimReconciler) handleDeployRuntmeClass(ctx context.Context, shim *kwasmv1.Shim) (ctrl.Result, error) {
	log.Info().Msgf("Deploying RuntimeClass: %s", shim.Spec.RuntimeClass.Name)
	rc, err := sr.createRuntimeClassManifest(shim)
	if err != nil {
		return ctrl.Result{}, err
	}

	// We want to use server-side apply https://kubernetes.io/docs/reference/using-api/server-side-apply
	patchMethod := client.Apply
	patchOptions := &client.PatchOptions{
		Force:        ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
		FieldManager: "shim-operator",
	}

	// Note that we reconcile even if the deployment is in a good state. We rely on controller-runtime to rate limit us.
	if err := sr.Client.Patch(ctx, rc, patchMethod, patchOptions); err != nil {
		log.Error().Msgf("Unable to reconcile RuntimeClass %s", err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// createRuntimeClassManifest creates a RuntimeClass manifest for a Shim.
func (sr *ShimReconciler) createRuntimeClassManifest(shim *kwasmv1.Shim) (*nodev1.RuntimeClass, error) {
	name := shim.Name
	nameMax := int(math.Min(float64(len(name)), 63))

	nodeSelector := shim.Spec.NodeSelector
	if nodeSelector == nil {
		nodeSelector = map[string]string{}
	}

	rc := &nodev1.RuntimeClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RuntimeClass",
			APIVersion: "node.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name[:nameMax],
			Namespace: os.Getenv("CONTROLLER_NAMESPACE"),
			Labels:    map[string]string{name[:nameMax]: "true"},
		},
		Handler: shim.Spec.RuntimeClass.Handler,
		Scheduling: &nodev1.Scheduling{
			NodeSelector: nodeSelector,
		},
	}

	if err := ctrl.SetControllerReference(shim, rc, sr.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	return rc, nil
}

// handleDeletion deletes all possible child resources of a Shim. It will ignore NotFound errors.
func (sr *ShimReconciler) handleDeletion(ctx context.Context, shim *kwasmv1.Shim) error {
	err := sr.deleteShim(ctx, shim)
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	return nil
}

// findShim finds a ShimResource.
func (sr *ShimReconciler) findShim(ctx context.Context, shim *kwasmv1.Shim) (*kwasmv1.Shim, error) {
	var s kwasmv1.Shim
	err := sr.Client.Get(ctx, types.NamespacedName{Name: shim.Name, Namespace: shim.Namespace}, &s)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// findJobsForShim finds all jobs related to a ShimResource.
func (sr *ShimReconciler) findJobsForShim(ctx context.Context, shim *kwasmv1.Shim) (*batchv1.JobList, error) {
	name := shim.Name + "-provisioner"
	nameMax := int(math.Min(float64(len(name)), 63))

	jobs := &batchv1.JobList{}

	err := sr.List(ctx, jobs, client.InNamespace(os.Getenv("CONTROLLER_NAMESPACE")), client.MatchingLabels(map[string]string{name[:nameMax]: "true"}))

	log.Debug().Msgf("Found %d jobs", len(jobs.Items))

	if err != nil {
		return nil, err
	}

	return jobs, nil
}

// deleteShim deletes a ShimResource.
func (sr *ShimReconciler) deleteShim(ctx context.Context, shim *kwasmv1.Shim) error {
	log.Info().Msgf("Deleting Shim... %s", shim.Name)

	s, err := sr.findShim(ctx, shim)
	if err != nil {
		return err
	}

	err = sr.deleteJobs(ctx, s)
	if err != nil {
		return err
	}

	// TODO: if Shim resource is deleted, it needs to be removed from nodes as well
	err = sr.Client.Delete(ctx, s)
	if err != nil {
		return err
	}

	log.Info().Msgf("Successfully deleted Shim... %s", shim.Name)

	return nil
}

// deleteJobs deletes all Jobs associated with a ShimResource.
func (sr *ShimReconciler) deleteJobs(ctx context.Context, shim *kwasmv1.Shim) error {
	jobsList, err := sr.findJobsForShim(ctx, shim)
	if err != nil {
		return err
	}

	for _, job := range jobsList.Items {
		err = sr.Client.Delete(ctx, &job)
		if err != nil {
			return err
		}
	}

	return nil
}

// runtimeClassExists checks whether a RuntimeClass for a Shim exists.
func (sr *ShimReconciler) runtimeClassExists(ctx context.Context, shim *kwasmv1.Shim) (bool, error) {
	if shim.Spec.RuntimeClass.Name != "" {
		rc, err := sr.findRuntimeClass(ctx, shim)
		if err != nil {
			log.Debug().Msgf("No RuntimeClass '%s' found", shim.Spec.RuntimeClass.Name)

			return false, err
		} else {
			log.Debug().Msgf("RuntimeClass found: %s", rc.Name)
			return true, nil
		}
	} else {
		log.Debug().Msg("RuntimeClass not defined")
		return false, nil
	}
}

// findRuntimeClass finds a RuntimeClass.
func (sr *ShimReconciler) findRuntimeClass(ctx context.Context, shim *kwasmv1.Shim) (*nodev1.RuntimeClass, error) {
	rc := nodev1.RuntimeClass{}
	err := sr.Client.Get(ctx, types.NamespacedName{Name: shim.Spec.RuntimeClass.Name, Namespace: shim.Namespace}, &rc)
	if err != nil {
		return nil, err
	}
	return &rc, nil
}

// removeFinalizer removes the finalizer from a Shim.
func (sr *ShimReconciler) removeFinalizer(ctx context.Context, shim *kwasmv1.Shim) error {
	if controllerutil.ContainsFinalizer(shim, KwasmOperatorFinalizer) {
		controllerutil.RemoveFinalizer(shim, KwasmOperatorFinalizer)
		if err := sr.Client.Update(ctx, shim); err != nil {
			return err
		}
	}
	return nil
}

func ptr[T any](v T) *T {
	return &v
}
