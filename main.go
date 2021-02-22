/*
MIT License

Copyright (c) 2018 Martin Linkhorst

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"context"
	"flag"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/spf13/pflag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/api/v1alpha1"
	"github.com/linki/cloudformation-operator/controllers"
	// +kubebuilder:scaffold:imports
)

var (
	scheme       = runtime.NewScheme()
	setupLog     = ctrl.Log.WithName("setup")
	StackFlagSet *pflag.FlagSet
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(cloudformationv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	StackFlagSet = pflag.NewFlagSet("stack", pflag.ExitOnError)
	StackFlagSet.String("region", "", "The AWS region to use")
	StackFlagSet.String("assume-role", "", "Assume AWS role when defined. Useful for stacks in another AWS account. Specify the full ARN, e.g. `arn:aws:iam::123456789:role/cloudformation-operator`")
	StackFlagSet.StringToString("tag", map[string]string{}, "Tags to apply to all Stacks by default. Specify multiple times for multiple tags.")
	StackFlagSet.StringSlice("capability", []string{}, "The AWS CloudFormation capability to enable")
	StackFlagSet.Bool("dry-run", false, "If true, don't actually do anything.")
}

func main() {
	var namespace string
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	flag.StringVar(&namespace, "namespace", "", "The Kubernetes namespace to watch")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.CommandLine.AddFlagSet(StackFlagSet)
	pflag.Parse()

	if namespace == "" {
		namespace = os.Getenv("WATCH_NAMESPACE")
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "64032969.cloudformation.linki.space",
		Namespace:              namespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	assumeRole, err := StackFlagSet.GetString("assume-role")
	if err != nil {
		setupLog.Error(err, "error parsing flag")
		os.Exit(1)
	}
	region, err := StackFlagSet.GetString("region")
	if err != nil {
		setupLog.Error(err, "error parsing flag")
		os.Exit(1)
	}
	defaultTags, err := StackFlagSet.GetStringToString("tag")
	if err != nil {
		setupLog.Error(err, "error parsing flag")
		os.Exit(1)
	}

	paramStringSlice, err := StackFlagSet.GetStringSlice("capability")
	if err != nil {
		setupLog.Error(err, "error parsing flag")
		os.Exit(1)
	}
	defaultCapabilities := make([]cfTypes.Capability, len(paramStringSlice))
	for i := range paramStringSlice {
		defaultCapabilities[i] = cfTypes.Capability(paramStringSlice[i])
	}

	dryRun, err := StackFlagSet.GetBool("dry-run")
	if err != nil {
		setupLog.Error(err, "error parsing flag")
		os.Exit(1)
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	if err != nil {
		setupLog.Error(err, "error getting AWS config")
		os.Exit(1)
	}
	creds := cfg.Credentials

	setupLog.Info(assumeRole)
	if assumeRole != "" {
		setupLog.Info("run assume")
		stsClient := sts.NewFromConfig(cfg)
		creds = stscreds.NewAssumeRoleProvider(stsClient, assumeRole)
	}

	client := cloudformation.NewFromConfig(cfg, func(o *cloudformation.Options) {
		o.Credentials = creds
	})

	cfHelper := &controllers.CloudFormationHelper{
		CloudFormation: client,
	}

	stackFollower := &controllers.StackFollower{
		Client:               mgr.GetClient(),
		Log:                  ctrl.Log.WithName("workers").WithName("Stack"),
		SubmissionChannel:    make(chan *cloudformationv1alpha1.Stack),
		CloudFormationHelper: cfHelper,
	}
	go stackFollower.Receiver()
	go stackFollower.Worker()

	if err = (&controllers.StackReconciler{
		Client:               mgr.GetClient(),
		Log:                  ctrl.Log.WithName("controllers").WithName("Stack"),
		Scheme:               mgr.GetScheme(),
		CloudFormation:       client,
		StackFollower:        stackFollower,
		CloudFormationHelper: cfHelper,
		DefaultTags:          defaultTags,
		DefaultCapabilities:  defaultCapabilities,
		DryRun:               dryRun,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Stack")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
