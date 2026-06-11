/*
Copyright 2025 mohammadreza.saberi@snapp.cab.

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
	"crypto/tls"
	"flag"
	"os"

	"github.com/snapp-incubator/namespacejanitor/internal/controller"
	"github.com/snapp-incubator/namespacejanitor/internal/notification"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	nativezap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	snappcloudv1alpha1 "github.com/snapp-incubator/namespacejanitor/api/v1alpha1"
	"gitlab.snapp.ir/platform/cloudgoutils/pkg/eventbus"
	// +kubebuilder:scaffold:impo
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(snappcloudv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var configPath string
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&configPath, "config", "/etc/namespacejanitor/config.yaml", "Path to the operator configuration file")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	operatorCfg, err := controller.LoadConfig(configPath)
	if err != nil {
		setupLog.Error(err, "unable to load operator configuration", "path", configPath)
		os.Exit(1)
	}
	setupLog.Info("Operator configuration loaded",
		"path", configPath,
		"yellowThreshold", operatorCfg.Lifecycle.YellowThreshold.Duration,
		"redThreshold", operatorCfg.Lifecycle.RedThreshold.Duration,
		"finalWarningThreshold", operatorCfg.Lifecycle.FinalWarningThreshold.Duration,
		"deleteThreshold", operatorCfg.Lifecycle.DeleteThreshold.Duration,
		"creationNotification", operatorCfg.Lifecycle.CreationNotification,
		"kafkaConfigured", operatorCfg.Notifications.Kafka.Broker != "",
		"mattermostConfigured", operatorCfg.Notifications.Mattermost.Webhook != "",
	)

	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "6c8304a1.snappcloud.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Build notifiers from config
	notifierLog := ctrl.Log.WithName("notifier")
	var notifiers []notification.Notifier

	// Kafka notifier
	if operatorCfg.Notifications.Kafka.Broker != "" {
		kafkaCfg := eventbus.KafkaConfig{
			Broker: operatorCfg.Notifications.Kafka.Broker,
			Topic:  operatorCfg.Notifications.Kafka.Topic,
			Group:  operatorCfg.Notifications.Kafka.Group,
		}
		zapLogger, err := nativezap.NewProduction()
		if err != nil {
			setupLog.Error(err, "unable to create zap logger for eventbus")
			os.Exit(1)
		}
		n, err := notification.New(kafkaCfg, zapLogger, notifierLog)
		if err != nil {
			setupLog.Error(err, "unable to setup kafka notifier")
			os.Exit(1)
		}
		notifiers = append(notifiers, n)
	}

	// Mattermost notifier
	if operatorCfg.Notifications.Mattermost.Webhook != "" {
		n, err := notification.NewMattermostNotifier(operatorCfg.Notifications.Mattermost.Webhook, notifierLog)
		if err != nil {
			setupLog.Error(err, "unable to setup mattermost notifier")
			os.Exit(1)
		}
		notifiers = append(notifiers, n)
	}

	// Build the final notifier
	var notifier notification.Notifier
	switch len(notifiers) {
	case 0:
		setupLog.Info("No notification channels configured. Running without notifications.")
		notifier = nil
	case 1:
		notifier = notifiers[0]
	default:
		notifier = notification.NewMultiNotifier(notifiers, notifierLog)
	}

	if notifier != nil {
		defer func() {
			if err := notifier.Close(); err != nil {
				setupLog.Error(err, "failed to close notifier connections")
			}
		}()
	}

	if err = (&controller.NamespaceJanitorReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Notifier: notifier,
		Config:   operatorCfg.Lifecycle,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NamespaceJanitor")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

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
