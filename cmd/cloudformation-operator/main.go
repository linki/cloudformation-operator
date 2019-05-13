package main

import (
	"context"
	"runtime"

	"github.com/alecthomas/kingpin"
	"github.com/sirupsen/logrus"

	"github.com/Scout24/cloudformation-operator/pkg/argparser"
	stub "github.com/Scout24/cloudformation-operator/pkg/stub"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
)

var (
	namespace    string
	region       string
	tags         = new(map[string]string)
	capabilities = []string{}
	dryRun       bool
	debug        bool
	version      = "0.4.0+git"
)

func init() {
	kingpin.Flag("namespace", "The Kubernetes namespace to watch").Default("default").Envar("WATCH_NAMESPACE").StringVar(&namespace)
	kingpin.Flag("region", "The AWS region to use").Envar("AWS_REGION").StringVar(&region)
	kingpin.Flag("capability", "The AWS CloudFormation capability to enable").Envar("AWS_CAPABILITIES").StringsVar(&capabilities)
	kingpin.Flag("dry-run", "If true, don't actually do anything.").Envar("DRY_RUN").BoolVar(&dryRun)
	kingpin.Flag("debug", "Enable debug logging.").Envar("DEBUG").BoolVar(&debug)

	tags = argparser.StringMap(kingpin.Flag("tag", "Tags to apply to all Stacks by default. Specify multiple times for multiple tags.").Envar("AWS_TAGS"))
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
	sdk.Handle(stub.NewHandler(client, capabilities, *tags, dryRun))
	sdk.Run(context.TODO())
}
