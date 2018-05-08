package main

import (
	"context"
	"runtime"

	"gopkg.in/alecthomas/kingpin.v2"



	"github.com/enekofb/cloudformation-operator/pkg/stub"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	log "github.com/sirupsen/logrus"
	"time"
)

const (
	ownerTagKey   = "kubernetes.io/controlled-by"
	ownerTagValue = "cloudformation.linki.space/operator"
)

var (
	master     string
	kubeconfig string
	region     string
	interval   time.Duration
	dryRun     bool
	debug      bool
	version    string
)

func init() {
	kingpin.Flag("master", "The address of the Kubernetes cluster to target").StringVar(&master)
	kingpin.Flag("kubeconfig", "Path to a kubeconfig file").StringVar(&kubeconfig)
	kingpin.Flag("region", "The AWS region to use").Default("us-west-2").StringVar(&region)
	kingpin.Flag("interval", "Interval between Stack synchronisations").Default("10m").DurationVar(&interval)
	kingpin.Flag("dry-run", "If true, don't actually do anything.").BoolVar(&dryRun)
	kingpin.Flag("debug", "Enable debug logging.").BoolVar(&debug)
}


func printVersion() {
	log.Infof("Go Version: %s",	 runtime.Version())
	log.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	log.Infof("operator-sdk Version: %v", sdkVersion.Version)
}

func main() {
	printVersion()

	kingpin.Version(version)
	kingpin.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	}

	if dryRun {
		log.Infof("Dry run enabled. I won't change anything.")
	}

	sdk.Watch("stacks.cloudformation.linki.space/v1alpha1", "Stack", "default", 5)
	sdk.Handle(stub.NewHandler(stub.NewParams(region,kubeconfig,dryRun,master)))
	sdk.Run(context.TODO())
}


