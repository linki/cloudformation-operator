package main

import (
	"context"
	"runtime"

	"github.com/alecthomas/kingpin"
	"github.com/sirupsen/logrus"

	"github.com/linki/cloudformation-operator/pkg/argparser"
	stub "github.com/linki/cloudformation-operator/pkg/stub"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/cloudformation/cloudformationiface"
)

var (
	namespace    string
	region       string
	assumeRole   string
	tags         = new(map[string]string)
	capabilities = []string{}
	dryRun       bool
	debug        bool
	version      = "0.4.0+git"
)

func init() {
	kingpin.Flag("namespace", "The Kubernetes namespace to watch").Default("default").Envar("WATCH_NAMESPACE").StringVar(&namespace)
	kingpin.Flag("region", "The AWS region to use").Envar("AWS_REGION").StringVar(&region)
	kingpin.Flag("assume-role", "Assume AWS role when defined. Useful for stacks in another AWS account. Specify the full ARN, e.g. `arn:aws:iam::123456789:role/cloudformation-operator`").Envar("AWS_ASSUME_ROLE").StringVar(&assumeRole)
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

	var client cloudformationiface.CloudFormationAPI
	sess := session.Must(session.NewSession())
	logrus.Info(assumeRole)
	if assumeRole != "" {
		logrus.Info("run assume")
		creds := stscreds.NewCredentials(sess, assumeRole)
		client = cloudformation.New(sess, &aws.Config{
			Credentials: creds,
			Region: aws.String(region),
		})
	} else {
		client = cloudformation.New(sess, &aws.Config{
			Region: aws.String(region),
		})
	}

	sdk.Watch("cloudformation.linki.space/v1alpha1", "Stack", namespace, 0)
	sdk.Handle(stub.NewHandler(client, capabilities, *tags, dryRun))
	sdk.Run(context.TODO())
}
