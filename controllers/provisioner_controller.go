/*
Copyright 2022.

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
	"os"
	"time"

	"github.com/rs/zerolog/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProvisionerReconciler reconciles a Provisioner object
type ProvisionerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Clock
}

type realClock struct{}

func (_ realClock) Now() time.Time { return time.Now() }

// clock knows how to get the current time.
// It can be used to fake out timing for testing.
type Clock interface {
	Now() time.Time
}

const (
	addKWasmNodeLabelAnnotation = "kwasm.sh/kwasm-node"
	nodeNameLabel               = "kwasm.sh/kwasm-provisioned"
	scheduledTimeAnnotation     = "kwasm.sh/scheduledAt"
	jobOwnerKey                 = ".metadata.controller"
)

//+kubebuilder:rbac:groups=wasm.kwasm.sh,resources=provisioners,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=wasm.kwasm.sh,resources=provisioners/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=wasm.kwasm.sh,resources=provisioners/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=list;get;watch;update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=list;get;create;watch;update;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Provisioner object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ProvisionerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.With().Str("node", req.Name).Logger()
	node := &corev1.Node{}

	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	/*
	   Step 1: Add or remove the label.
	*/

	labelShouldBePresent := node.Annotations[addKWasmNodeLabelAnnotation] == "true"
	labelIsPresent := node.Labels[nodeNameLabel] == node.Name

	if labelShouldBePresent == labelIsPresent {
		// The desired state and actual state of the Pod are the same.
		// No further action is required by the operator at this moment.

		return ctrl.Result{}, nil
	}
	if labelShouldBePresent {
		// If the label should be set but is not, set it.
		if node.Labels == nil {
			node.Labels = make(map[string]string)
		}
		node.Labels[nodeNameLabel] = node.Name
		log.Info().Msgf("Trying to Deploy on %s", node.Name)
		dep := r.deployJob(node, req)
		err := r.Create(ctx, dep)
		if err != nil {
			log.Err(err).Msg("Failed to create new Job " + req.Namespace + " Job.Name " + req.Name)
			return ctrl.Result{}, err
		}

	} else {
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
			log.Info().Msg("Node IsConflict, Requeuing")
			// The Pod has been updated since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, nil
		}
		if apierrors.IsNotFound(err) {
			log.Info().Msg("Node IsNotFound, Requeuing")

			// The Pod has been deleted since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error().Err(err).Msg("unable to update Node")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ProvisionerReconciler) deployJob(n *corev1.Node, req ctrl.Request) *batchv1.Job {

	priv := true

	dep := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name + "-provision-kwasm",
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
	ctrl.SetControllerReference(n, dep, r.Scheme)

	return dep
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProvisionerReconciler) SetupWithManager(mgr ctrl.Manager) error {

	//if err := mgr.GetFieldIndexer().IndexField(context.Background(), &batchv1.Job{}, ".metadata.name", func(rawObj client.Object) []string {
	// grab the job object, extract the owner...

	// 	job := rawObj.(*batchv1.Job)
	// 	owner := metav1.GetControllerOf(job)
	// 	if owner == nil {
	// 		return nil
	// 	}
	// 	_, finishedType := r.isJobFinished(job)
	// 	switch finishedType {
	// 	case "": // ongoing
	// 		log.Info().Msgf("Job %s is still Ongoing", job.Name)
	// 		return nil
	// 	case batchv1.JobFailed:
	// 		log.Info().Msgf("Job %s is still failing...", job.Name)
	// 		return nil
	// 	case batchv1.JobComplete:
	// 		log.Info().Msgf("Job %s is Completed. Happy WASMing", job.Name)
	// 		return nil
	// 	}
	// 	return nil
	// }); err != nil {
	// 	return err
	// }

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}

func (r *ProvisionerReconciler) isJobFinished(job *batchv1.Job) (bool, batchv1.JobConditionType) {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true, c.Type
		}
	}

	return false, ""
}
