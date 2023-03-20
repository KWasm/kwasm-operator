package controllers_test

import (
	"context"

	"github.com/kwasm/kwasm-operator/controllers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("JobController", func() {
	Context("Kwasm Provisioner Job controller test", func() {
		var (
			ctx  context.Context
			node *corev1.Node
			//scheme     *runtime.Scheme
			//err error
			//request    ctrl.Request
			//result ctrl.Result
		)
		ctx = context.Background()
		It("should create runtimeclass if CreateRuntimeClass is set", func() {
			nodeName := "runtimeclass-node"
			nodeNameNamespaced := types.NamespacedName{Name: nodeName, Namespace: ""}

			runtimeClassName := "crun"
			runtimeClassNamespaced := types.NamespacedName{Name: runtimeClassName, Namespace: ""}

			jobNameNamespaced := types.NamespacedName{Name: nodeName + "-provision-kwasm", Namespace: namespaceName}
			JobReconciler := &controllers.JobReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				CreateRuntimeClass: true,
			}
			kwasmReconciler := &controllers.ProvisionerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Annotations: map[string]string{
						"kwasm.sh/kwasm-node": "true",
					},
				},
			}
			err := k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			_, err = kwasmReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: nodeNameNamespaced,
			})
			_, err = JobReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: jobNameNamespaced,
			})
			// Check that the runtimeclass was created.
			runtimeClass := &nodev1.RuntimeClass{}
			err = k8sClient.Get(ctx, runtimeClassNamespaced, runtimeClass)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
