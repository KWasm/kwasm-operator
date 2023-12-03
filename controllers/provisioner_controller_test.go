package controllers_test

import (
	"context"

	"github.com/kwasm/kwasm-operator/controllers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var namespaceName = "kwasm-provisioner"
var installerImage = "ghcr.io/kwasm/kwasm-node-installer:latest"

var _ = Describe("ProvisionerController", func() {
	Context("Kwasm Provisioner controller test", func() {
		var (
			ctx    context.Context
			node   *corev1.Node
			err    error
			result ctrl.Result
			job    *batchv1.Job
		)

		ctx = context.Background()

		It("should reconcile the node and create a job", func() {
			var nodeName = "test-node-with-annotation"
			nodeNameNamespaced := types.NamespacedName{Name: nodeName, Namespace: ""}
			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Annotations: map[string]string{
						"kwasm.sh/kwasm-node": "true",
					},
				},
			}

			kwasmReconciler := &controllers.ProvisionerReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				InstallerImage: installerImage,
			}
			err = k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			// Call Reconcile with a fake Request object.
			request := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespaceName,
				},
			}
			_, err = kwasmReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: nodeNameNamespaced,
			})
			Expect(err).To(Not(HaveOccurred()))

			// Verify that the Node has the expected label.
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Labels["kwasm.sh/kwasm-provisioned"]).To(Equal(nodeName))

			// Call Reconcile again with the same Request object to ensure that the operator
			// does not create a duplicate Job.
			result, err = kwasmReconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Check that the job was created.
			job = &batchv1.Job{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-provision-kwasm", Namespace: namespaceName}, job)
			Expect(err).NotTo(HaveOccurred())
			Expect(job).NotTo(BeNil())
		})

		It("should not set autoProvision to true by default", func() {
			r := controllers.ProvisionerReconciler{}.AutoProvision
			Expect(r).To(BeFalse())
		})

		It("Should ignore Nodes with no annotation", func() {
			nodeName := "test-node-without-annotation"
			nodeNameNamespaced := types.NamespacedName{Name: nodeName, Namespace: ""}

			kwasmReconciler := &controllers.ProvisionerReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				InstallerImage: installerImage,
			}

			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}
			err := k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			_, err = kwasmReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: nodeNameNamespaced,
			})
			Expect(err).To(Not(HaveOccurred()))

			// Check that the job was created.
			job = &batchv1.Job{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-provision-kwasm", Namespace: namespaceName}, job)
			Expect(err).To(HaveOccurred())
		})

		It("should provision node if autoprovision is true", func() {
			nodeName := "autoprovision-node"
			nodeNameNamespaced := types.NamespacedName{Name: nodeName, Namespace: ""}

			kwasmReconciler := &controllers.ProvisionerReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				InstallerImage: installerImage,
				AutoProvision:  true,
			}
			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}
			err := k8sClient.Create(ctx, node)
			Expect(err).NotTo(HaveOccurred())

			_, err = kwasmReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: nodeNameNamespaced,
			})
			Expect(err).NotTo(HaveOccurred())

			// Check that the job was created.
			job = &batchv1.Job{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: nodeName + "-provision-kwasm", Namespace: namespaceName}, job)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
