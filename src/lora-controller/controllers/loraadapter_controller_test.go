package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	lorav1alpha1 "production-stack.vllm.ai/lora-controller/api/v1alpha1"
	"production-stack.vllm.ai/lora-controller/pkg/placement"
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
})

var _ = Describe("LoraAdapter Controller", func() {
	var (
		ctx        context.Context
		k8sClient  client.Client
		reconciler *LoraAdapterReconciler
		mockServer *httptest.Server
		testScheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		testScheme = runtime.NewScheme()
		Expect(scheme.AddToScheme(testScheme)).To(Succeed())
		Expect(lorav1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(apiextensionsv1.AddToScheme(testScheme)).To(Succeed())

		// Create a mock HTTP server
		mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "success"}`))
		}))

		// Create fake client with the scheme
		k8sClient = fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vllm-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							"app":   "vllm",
							"model": "llama2-7b",
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						PodIP: "10.0.0.1",
					},
				},
			).
			WithStatusSubresource(&lorav1alpha1.LoraAdapter{}).
			Build()

		reconciler = &LoraAdapterReconciler{
			Client:       k8sClient,
			Scheme:       testScheme,
			Algorithm:    placement.NewDefaultAlgorithm(k8sClient, "default"),
			testEndpoint: mockServer.URL + "/v1/load_lora_adapter",
		}
	})

	AfterEach(func() {
		if mockServer != nil {
			mockServer.Close()
		}
	})

	Context("When reconciling a LoraAdapter", func() {
		It("Should handle local adapter source", func() {
			adapter := &lorav1alpha1.LoraAdapter{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-local-adapter",
					Namespace: "default",
				},
				Spec: lorav1alpha1.LoraAdapterSpec{
					BaseModel: "llama2-7b",
					AdapterSource: lorav1alpha1.AdapterSource{
						Type:        "local",
						Repository:  "file://local",
						AdapterPath: "/models/test-adapter",
						AdapterName: "test-adapter",
					},
					DeploymentConfig: lorav1alpha1.DeploymentConfig{
						Algorithm: "default",
					},
				},
			}

			// Create the adapter first
			Expect(k8sClient.Create(ctx, adapter)).To(Succeed())

			// Wait for the adapter to be created
			createdAdapter := &lorav1alpha1.LoraAdapter{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      adapter.Name,
					Namespace: adapter.Namespace,
				}, createdAdapter)
			}).Should(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      adapter.Name,
					Namespace: adapter.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).To(BeNil())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify status
			var updatedAdapter lorav1alpha1.LoraAdapter
			Eventually(func() string {
				err := k8sClient.Get(ctx, req.NamespacedName, &updatedAdapter)
				if err != nil {
					return ""
				}
				return updatedAdapter.Status.Phase
			}).Should(Equal("Ready"))

			Expect(updatedAdapter.Status.LoadedAdapters).To(HaveLen(1))
			Expect(updatedAdapter.Status.LoadedAdapters[0].Path).To(Equal("/models/test-adapter"))
		})

		It("Should validate required fields", func() {
			adapter := &lorav1alpha1.LoraAdapter{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-invalid",
					Namespace: "default",
				},
				Spec: lorav1alpha1.LoraAdapterSpec{
					BaseModel: "llama2-7b",
					AdapterSource: lorav1alpha1.AdapterSource{
						Type:       "local",
						Repository: "file://local",
						// Missing AdapterPath for local source
						AdapterName: "test-adapter",
					},
					DeploymentConfig: lorav1alpha1.DeploymentConfig{
						Algorithm: "default",
					},
				},
			}

			// Create the adapter first
			Expect(k8sClient.Create(ctx, adapter)).To(Succeed())

			// Wait for the adapter to be created
			createdAdapter := &lorav1alpha1.LoraAdapter{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      adapter.Name,
					Namespace: adapter.Namespace,
				}, createdAdapter)
			}).Should(Succeed())

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      adapter.Name,
					Namespace: adapter.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).To(MatchError("adapterPath is required for local adapter source"))
			Expect(result).To(Equal(ctrl.Result{RequeueAfter: time.Minute}))

			// Verify the status was updated
			updatedAdapter := &lorav1alpha1.LoraAdapter{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, req.NamespacedName, updatedAdapter)
				if err != nil {
					return ""
				}
				return updatedAdapter.Status.Phase
			}).Should(Equal("Pending"))
		})
	})
})
