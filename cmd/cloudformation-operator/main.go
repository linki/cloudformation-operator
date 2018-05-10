package main

import (
	"context"
	"runtime"

	"github.com/alecthomas/kingpin"
	"github.com/sirupsen/logrus"

	stub "github.com/linki/cloudformation-operator/pkg/stub"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
)

var (
	namespace string
	region    string
	dryRun    bool
	debug     bool
	version   = "0.2.0+git"
)

func init() {
	kingpin.Flag("namespace", "The Kubernetes namespace to watch").Default("default").StringVar(&namespace)
	kingpin.Flag("region", "The AWS region to use").Required().StringVar(&region)
	kingpin.Flag("dry-run", "If true, don't actually do anything.").BoolVar(&dryRun)
	kingpin.Flag("debug", "Enable debug logging.").BoolVar(&debug)
}

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
	logrus.Infof("cloudformation-operator Version: %v", version)
}

func main() {
	kingpin.Version(version)
	kingpin.Parse()

	if debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if dryRun {
		logrus.Info("Dry run enabled. I won't change anything.")
	}

	printVersion()

	client := cloudformation.New(session.New(), &aws.Config{
		Region: aws.String(region),
	})

	sdk.Watch("cloudformation.linki.space/v1alpha1", "Stack", namespace, 0)
	sdk.Handle(stub.NewHandler(client, dryRun))
	sdk.Run(context.TODO())
}
