module github.com/linki/cloudformation-operator

go 1.15

require (
	github.com/aws/aws-sdk-go-v2 v1.3.0
	github.com/aws/aws-sdk-go-v2/config v1.1.3
	github.com/aws/aws-sdk-go-v2/credentials v1.1.3
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.2.0
	github.com/aws/aws-sdk-go-v2/service/sts v1.2.0
	github.com/go-logr/logr v1.2.3
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.19.0
	github.com/spf13/pflag v1.0.5
	k8s.io/apimachinery v0.25.0
	k8s.io/client-go v0.25.0
	sigs.k8s.io/controller-runtime v0.13.1
)
