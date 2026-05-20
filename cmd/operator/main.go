// Command operator is the manager binary for the ObservabilityPack
// meta-operator. It wires controller-runtime to the PackReconciler
// and the Azure HTTP trigger, and exposes /healthz, /readyz, and
// /metrics on the configured ports.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
	"github.com/example/observability-pack/internal/operator/controller"
	"github.com/example/observability-pack/internal/operator/httptrigger"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiv1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		leaderElectionID     string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address the metric endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address the probe endpoint binds to")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "enable leader election for controller manager")
	flag.StringVar(&leaderElectionID, "leader-election-id", "observability-pack-operator", "leader election lock id")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID,
	})
	if err != nil {
		setupExit(err, "create manager")
	}

	if err := (&controller.PackReconciler{
		Client:   mgr.GetClient(),
		Azure:    httptrigger.New(mgr.GetClient()),
		Recorder: mgr.GetEventRecorderFor("observability-pack-operator"),
	}).SetupWithManager(mgr); err != nil {
		setupExit(err, "setup PackReconciler")
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		setupExit(err, "add healthz")
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		setupExit(err, "add readyz")
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupExit(err, "manager exited")
	}
}

func setupExit(err error, msg string) {
	ctrl.Log.Error(err, msg)
	os.Exit(1)
}
