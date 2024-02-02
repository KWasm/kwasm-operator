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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=runtime.kwasm.sh,resources=shims/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (sr *ShimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kwasmv1.Shim{}).
		// As we create and own the created jobs
		// Jobs are important for us to update the Shims installation status
		// on respective nodes
		Owns(&batchv1.Job{}).
		// As we don't own nodes, but need to react on node label changes,
		// we need to watch node label changes.
		// Whenever a label changes, we want to reconcile Shims, to make sure
		// that the shim is deployed on the node if it should be.
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(sr.findShimsToReconcile),
			builder.WithPredicates(predicate.LabelChangedPredicate{}),
		).
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
	ctx = log.WithContext(ctx)

	// 1. Check if the shim resource exists
	var shimResource kwasmv1.Shim
	if err := sr.Client.Get(ctx, req.NamespacedName, &shimResource); err != nil {
		log.Err(err).Msg("Unable to fetch shimResource")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Get list of nodes where this shim is supposed to be deployed on
	nodes := &corev1.NodeList{}
	if shimResource.Spec.NodeSelector != nil {
		// 3.1 that match the nodeSelector
		err := sr.List(ctx, nodes, client.InNamespace(req.Namespace), client.MatchingLabels(shimResource.Spec.NodeSelector))
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// 3.2 or no selector at all (all nodes)
		err := sr.List(ctx, nodes, client.InNamespace(req.Namespace))
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// TODO: Update the number of nodes that are relevant to this shim
	err := sr.updateStatus(ctx, &shimResource, nodes)
	if err != nil {
		log.Error().Msgf("Unable to update node count: %s", err)
		return ctrl.Result{}, err
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

	// 3. Check if referenced runtimeClass exists in cluster
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

// findShimsToReconcile finds all Shims that need to be reconciled.
// This function is required e.g. to react on node label changes.
// When the label of a node changes, we want to reconcile shims to make sure
// that the shim is deployed on the node if it should be.
func (sr *ShimReconciler) findShimsToReconcile(ctx context.Context, node client.Object) []reconcile.Request {
	shimList := &kwasmv1.ShimList{}
	listOps := &client.ListOptions{
		Namespace: "",
	}
	err := sr.List(context.TODO(), shimList, listOps)
	if err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(shimList.Items))
	for i, item := range shimList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      item.GetName(),
				Namespace: item.GetNamespace(),
			},
		}
	}
	return requests
}

func (sr *ShimReconciler) updateStatus(ctx context.Context, shim *kwasmv1.Shim, nodes *corev1.NodeList) error {
	log := log.Ctx(ctx)

	shim.Status.NodeCount = len(nodes.Items)
	shim.Status.NodeReadyCount = 0

	if len(nodes.Items) >= 0 {
		for _, node := range nodes.Items {
			if node.Labels[shim.Name] == "provisioned" {
				shim.Status.NodeReadyCount++
			}
		}
	}

	if err := sr.Update(ctx, shim); err != nil {
		log.Error().Msgf("Unable to update status %s", err)
	}

	// Re-fetch shim to avoid "object has been modified" errors
	if err := sr.Client.Get(ctx, types.NamespacedName{Name: shim.Name, Namespace: shim.Namespace}, shim); err != nil {
		log.Error().Msgf("Unable to re-fetch app: %s", err)
		return err
	}

	return nil
}

// handleDeployJob deploys a Job to each node in a list.
func (sr *ShimReconciler) handleDeployJob(ctx context.Context, shim *kwasmv1.Shim, nodes *corev1.NodeList, req ctrl.Request) (ctrl.Result, error) {
	log := log.Ctx(ctx)

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

				shimProvisioned := node.Labels[shim.Name] == "provisioned"
				shimPending := node.Labels[shim.Name] == "pending"

				if !shimProvisioned && !shimPending {

					err := sr.deployJobOnNode(ctx, shim, node, req)
					if err != nil {
						return ctrl.Result{}, err
					}

				} else {
					log.Info().Msgf("Shim %s already provisioned on Node %s", shim.Name, node.Name)
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

// deployJobOnNode deploys a Job to a Node.
func (sr *ShimReconciler) deployJobOnNode(ctx context.Context, shim *kwasmv1.Shim, node corev1.Node, req ctrl.Request) error {
	log := log.Ctx(ctx)

	log.Info().Msgf("Deploying Shim %s on node: %s", shim.Name, node.Name)

	if err := sr.updateNodeLabels(ctx, &node, shim, "pending", req); err != nil {
		log.Error().Msgf("Unable to update node label %s: %s", shim.Name, err)
	}

	job, err := sr.createJobManifest(shim, &node, req)
	if err != nil {
		return err
	}

	// We want to use server-side apply https://kubernetes.io/docs/reference/using-api/server-side-apply
	patchMethod := client.Apply
	patchOptions := &client.PatchOptions{
		Force:        ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
		FieldManager: "shim-operator",
	}

	// We rely on controller-runtime to rate limit us.
	if err := sr.Client.Patch(ctx, job, patchMethod, patchOptions); err != nil {
		log.Error().Msgf("Unable to reconcile Job %s", err)
		if err := sr.updateNodeLabels(ctx, &node, shim, "failed", req); err != nil {
			log.Error().Msgf("Unable to update node label %s: %s", shim.Name, err)
		}
		return err
	}

	return nil
}

func (sr *ShimReconciler) updateNodeLabels(ctx context.Context, node *corev1.Node, shim *kwasmv1.Shim, status string, req ctrl.Request) error {
	node.Labels[shim.Name] = status

	if err := sr.Update(ctx, node); err != nil {
		return err
	}

	return nil
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
			Labels: map[string]string{
				name[:nameMax]:      "true",
				"kwasm.sh/shimName": shim.Name,
				"kwasm.sh/job":      "true",
			},
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
	log := log.Ctx(ctx)

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
	log := log.Ctx(ctx)

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
	log := log.Ctx(ctx)
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
	log := log.Ctx(ctx)

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
