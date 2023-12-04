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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kwasm/kwasm-operator/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(batchv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

// getWatchNamespace returns the Namespace the operator should be watching for changes
func getWatchNamespace() string {
	// WatchNamespaceEnvVar is the constant for env variable WATCH_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator will fail to start.
	var watchNamespaceEnvVar = "CONTROLLER_NAMESPACE"

	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		panic(fmt.Sprintf("env var '%s' must be set", watchNamespaceEnvVar))
	}
	return ns
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	namespaceSelector := cache.ByObject{
		Field: fields.ParseSelectorOrDie(fmt.Sprintf("metadata.namespace=%s", getWatchNamespace())),
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "c74b86db.kwasm.sh",
		// Warning: the manager creates a client, which then uses Watches to monitor
		// certain resources. By default, the client is not going to be namespaced,
		// it will be able to watch resources across the entire cluster. This is of
		// course constrained by the RBAC rules applied to the ServiceAccount that
		// runs the controller.
		// **However**, even when accessing a resource inside of a specific Namespace,
		// the default behaviour of the cache is to create a Watch that is not namespaced;
		// hence requires the privilege to access all the resources of that type inside
		// of the cluster. That can cause runtime error if the ServiceAccount lacking
		// this privilege.
		// For example, when we access a Job inside the `kwasm`
		// namespace, the cache will create a Watch against Job, that will require
		// privileged to acccess ALL the secrets of the cluster.
		//
		// To be able to have stricter RBAC rules, we need to instruct the cache to
		// only watch objects inside of the namespace where the controller is running.
		// That applies ONLY to the namespaced resources that we know the controller
		// is going to own inside of a specific namespace.
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&batchv1.Job{}: namespaceSelector,
			},
		},
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	autoProvision := false
	if autoProvisionEnv, found := os.LookupEnv("AUTO_PROVISION_NODES"); found && strings.ToLower(autoProvisionEnv) == "true" {
		autoProvision = true
		setupLog.Info("AUTO_PROVISION_NODES enabled")
	}

	installerImage := "ghcr.io/kwasm/kwasm-node-installer:main"
	if installerImageEnv, found := os.LookupEnv("INSTALLER_IMAGE"); found {
		installerImage = installerImageEnv
		setupLog.Info(fmt.Sprintf("INSTALLER_IMAGE=%s", installerImage))
	}

	if err = (&controllers.ProvisionerReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		AutoProvision:  autoProvision,
		InstallerImage: installerImage,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Provisioner")
		os.Exit(1)
	}
	if err = (&controllers.JobReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Job")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
