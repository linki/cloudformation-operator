module github.com/linki/cloudformation-operator

go 1.15

require (
	github.com/aws/aws-sdk-go-v2 v1.3.0
	github.com/aws/aws-sdk-go-v2/config v1.1.1
	github.com/aws/aws-sdk-go-v2/credentials v1.1.1
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.1.1
	github.com/aws/aws-sdk-go-v2/service/sts v1.2.0
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/spf13/pflag v1.0.5
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.19.2
	sigs.k8s.io/controller-runtime v0.7.0
)
