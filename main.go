package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/cloudformation/cloudformationiface"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/linki/cloudformation-operator/pkg/apis/cloudformation/v1alpha1"
	clientset "github.com/linki/cloudformation-operator/pkg/client/clientset/versioned"
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
	kingpin.Flag("region", "The AWS region to use").StringVar(&region)
	kingpin.Flag("interval", "Interval between Stack synchronisations").Default("10m").DurationVar(&interval)
	kingpin.Flag("dry-run", "If true, don't actually do anything.").BoolVar(&dryRun)
	kingpin.Flag("debug", "Enable debug logging.").BoolVar(&debug)
}

func main() {
	kingpin.Version(version)
	kingpin.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	}

	if dryRun {
		log.Infof("Dry run enabled. I won't change anything.")
	}

	svc := cloudformation.New(session.New(), &aws.Config{
		Region: aws.String(region),
	})

	client, err := newClient()
	if err != nil {
		log.Fatal(err)
	}

	for {
		fmt.Println("current stacks:")
		currentStacks := getCurrentStacks(svc)
		for _, stack := range currentStacks {
			fmt.Printf("  %s (%s)\n", aws.StringValue(stack.StackName), aws.StringValue(stack.StackId))
		}

		fmt.Println("desired stacks:")
		desiredStacks := getDesiredStacks(client)
		for _, stack := range desiredStacks.Items {
			fmt.Printf("  %s/%s\n", stack.Namespace, stack.Name)
		}

		fmt.Println("matching stacks:")
		matchingStacks := getMatchingStacks(currentStacks, desiredStacks)
		for _, stack := range matchingStacks.Items {
			fmt.Printf("  %s/%s\n", stack.Namespace, stack.Name)
		}

		fmt.Println("superfluous stacks:")
		superfluousStacks := getSuperfluousStacks(currentStacks, desiredStacks)
		for _, stack := range superfluousStacks {
			fmt.Printf("  %s (%s)\n", aws.StringValue(stack.StackName), aws.StringValue(stack.StackId))
		}

		fmt.Println("missing stacks:")
		missingStacks := getMissingStacks(currentStacks, desiredStacks)
		for _, stack := range missingStacks.Items {
			fmt.Printf("  %s/%s\n", stack.Namespace, stack.Name)
		}

		for _, stack := range matchingStacks.Items {
			updateStack(svc, client, stack)
		}

		for _, stack := range superfluousStacks {
			deleteStack(svc, stack)
		}

		for _, stack := range missingStacks.Items {
			createStack(svc, client, stack)
		}

		time.Sleep(interval)
	}
}

func getCurrentStacks(svc cloudformationiface.CloudFormationAPI) []*cloudformation.Stack {
	stacks, err := svc.DescribeStacks(&cloudformation.DescribeStacksInput{})
	if err != nil {
		log.Fatal(err)
	}

	ownedStacks := []*cloudformation.Stack{}

	for _, stack := range stacks.Stacks {
		for _, tag := range stack.Tags {
			if aws.StringValue(tag.Key) == ownerTagKey && aws.StringValue(tag.Value) == ownerTagValue {
				ownedStacks = append(ownedStacks, stack)
			}
		}
	}

	return ownedStacks
}

func getDesiredStacks(client clientset.Interface) *v1alpha1.StackList {
	stackList, err := client.CloudformationV1alpha1().Stacks(v1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}

	return stackList
}

func getMatchingStacks(current []*cloudformation.Stack, desired *v1alpha1.StackList) v1alpha1.StackList {
	stackList := v1alpha1.StackList{Items: []v1alpha1.Stack{}}

	for _, ds := range desired.Items {
		for _, cs := range current {
			if aws.StringValue(cs.StackName) == ds.Name {
				stackList.Items = append(stackList.Items, ds)
			}
		}
	}

	return stackList
}

func getSuperfluousStacks(current []*cloudformation.Stack, desired *v1alpha1.StackList) []*cloudformation.Stack {
	stacks := []*cloudformation.Stack{}

	for _, cs := range current {
		found := false

		for _, ds := range desired.Items {
			if aws.StringValue(cs.StackName) == ds.Name {
				found = true
			}
		}

		if !found {
			stacks = append(stacks, cs)
		}
	}

	return stacks
}

func getMissingStacks(current []*cloudformation.Stack, desired *v1alpha1.StackList) v1alpha1.StackList {
	stackList := v1alpha1.StackList{
		Items: []v1alpha1.Stack{},
	}

	for _, ds := range desired.Items {
		found := false

		for _, cs := range current {
			if aws.StringValue(cs.StackName) == ds.Name {
				found = true
			}
		}

		if !found {
			stackList.Items = append(stackList.Items, ds)
		}
	}

	return stackList
}

func createStack(svc cloudformationiface.CloudFormationAPI, client clientset.Interface, stack v1alpha1.Stack) {
	fmt.Printf("creating stack: %s\n", stack.Name)

	if dryRun {
		fmt.Println("skipping...")
		return
	}

	params := []*cloudformation.Parameter{}
	for k, v := range stack.Spec.Parameters {
		params = append(params, &cloudformation.Parameter{
			ParameterKey:   aws.String(k),
			ParameterValue: aws.String(v),
		})
	}

	input := &cloudformation.CreateStackInput{
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   params,
		Tags: []*cloudformation.Tag{
			{
				Key:   aws.String(ownerTagKey),
				Value: aws.String(ownerTagValue),
			},
		},
	}
	if _, err := svc.CreateStack(input); err != nil {
		log.Fatal(err)
	}

	for {
		foundStack := getStack(svc, stack.Name)

		fmt.Printf("Stack status: %s\n", aws.StringValue(foundStack.StackStatus))

		if aws.StringValue(foundStack.StackStatus) != cloudformation.StackStatusCreateInProgress {
			break
		}

		time.Sleep(time.Second)
	}

	foundStack := getStack(svc, stack.Name)

	stackCopy := stack.DeepCopy()
	stackCopy.Status.StackID = aws.StringValue(foundStack.StackId)

	stackCopy.Status.Outputs = map[string]string{}
	for _, output := range foundStack.Outputs {
		stackCopy.Status.Outputs[aws.StringValue(output.OutputKey)] = aws.StringValue(output.OutputValue)
	}

	if _, err := client.CloudformationV1alpha1().Stacks(stack.Namespace).Update(stackCopy); err != nil {
		log.Fatal(err)
	}
}

func updateStack(svc cloudformationiface.CloudFormationAPI, client clientset.Interface, stack v1alpha1.Stack) {
	fmt.Printf("updating stack: %s\n", stack.Name)

	if dryRun {
		fmt.Println("skipping...")
	}

	params := []*cloudformation.Parameter{}
	for k, v := range stack.Spec.Parameters {
		params = append(params, &cloudformation.Parameter{
			ParameterKey:   aws.String(k),
			ParameterValue: aws.String(v),
		})
	}

	input := &cloudformation.UpdateStackInput{
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   params,
	}

	if _, err := svc.UpdateStack(input); err != nil {
		if strings.Contains(err.Error(), "No updates are to be performed.") {
			fmt.Println("Stack update not needed.")
			return
		}
		log.Fatal(err)
	}

	for {
		foundStack := getStack(svc, stack.Name)

		fmt.Printf("Stack status: %s\n", aws.StringValue(foundStack.StackStatus))

		if aws.StringValue(foundStack.StackStatus) != cloudformation.StackStatusUpdateInProgress {
			break
		}

		time.Sleep(time.Second)
	}

	foundStack := getStack(svc, stack.Name)

	stackCopy := stack.DeepCopy()
	stackCopy.Status.StackID = aws.StringValue(foundStack.StackId)

	stackCopy.Status.Outputs = map[string]string{}
	for _, output := range foundStack.Outputs {
		stackCopy.Status.Outputs[aws.StringValue(output.OutputKey)] = aws.StringValue(output.OutputValue)
	}

	if _, err := client.CloudformationV1alpha1().Stacks(stack.Namespace).Update(stackCopy); err != nil {
		log.Fatal(err)
	}
}

func deleteStack(svc cloudformationiface.CloudFormationAPI, stack *cloudformation.Stack) {
	fmt.Printf("deleting stack: %s\n", aws.StringValue(stack.StackName))

	if dryRun {
		fmt.Println("skipping...")
	}

	input := &cloudformation.DeleteStackInput{
		StackName: stack.StackName,
	}

	if _, err := svc.DeleteStack(input); err != nil {
		log.Fatal(err)
	}

	for {
		foundStack := getStack(svc, aws.StringValue(stack.StackName))

		if foundStack == nil {
			break
		}

		fmt.Printf("Stack status: %s\n", aws.StringValue(foundStack.StackStatus))

		if aws.StringValue(foundStack.StackStatus) != cloudformation.StackStatusDeleteInProgress {
			break
		}

		time.Sleep(time.Second)
	}
}

func getStack(svc cloudformationiface.CloudFormationAPI, name string) *cloudformation.Stack {
	resp, err := svc.DescribeStacks(&cloudformation.DescribeStacksInput{
		StackName: aws.String(name),
	})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		log.Fatal(err)
	}
	if len(resp.Stacks) == 0 {
		return nil
	}

	return resp.Stacks[0]
}

func newClient() (*clientset.Clientset, error) {
	if kubeconfig == "" {
		if _, err := os.Stat(clientcmd.RecommendedHomeFile); err == nil {
			kubeconfig = clientcmd.RecommendedHomeFile
		}
	}

	config, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		return nil, err
	}

	log.Infof("Targeting cluster at %s", config.Host)

	client, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return client, nil
}
