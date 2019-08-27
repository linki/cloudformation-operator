package stack

import (
	"context"
	coreerrors "errors"
	"reflect"
	"strings"
	"time"

	// "github.com/alecthomas/kingpin"
	// "github.com/operator-framework/operator-sdk/pkg/sdk/action"
	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/pkg/apis/cloudformation/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	// "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/cloudformation/cloudformationiface"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ownerTagKey   = "kubernetes.io/controlled-by"
	ownerTagValue = "cloudformation.linki.space/operator"
)

var (
	ErrStackNotFound = coreerrors.New("stack not found")
)

// TODO: remove me
var (
	Cloudformation cloudformationiface.CloudFormationAPI
	Tags           = map[string]string{}
	Capabilities   = []string{}
	DryRun         bool
)

var log = logf.Log.WithName("controller_stack")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new Stack Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileStack{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("stack-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Stack
	err = c.Watch(&source.Kind{Type: &cloudformationv1alpha1.Stack{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileStack implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileStack{}

// ReconcileStack reconciles a Stack object
type ReconcileStack struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Stack object and makes changes based on the state read
// and what is in the Stack.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileStack) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Stack")

	// Fetch the Stack instance
	instance := &cloudformationv1alpha1.Stack{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			stack := &cloudformationv1alpha1.Stack{
				ObjectMeta: metav1.ObjectMeta{
					Name:      request.NamespacedName.Name,
					Namespace: request.NamespacedName.Namespace,
				},
			}

			if err := r.deleteStack(stack); err != nil {
				return reconcile.Result{}, err
			}

			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	exists, err := r.stackExists(instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	if exists {
		return reconcile.Result{}, r.updateStack(instance)
	}

	return reconcile.Result{}, r.createStack(instance)
}

func (r *ReconcileStack) createStack(stack *cloudformationv1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("creating stack")

	if DryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack creation")
		return nil
	}

	input := &cloudformation.CreateStackInput{
		Capabilities: aws.StringSlice(Capabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   stackParameters(stack),
		Tags:         stackTags(stack, Tags),
	}

	// TODO: set owner reference on CF stack for "garbage collection"
	// remove stack wwhen owner is gone

	// // Set Stack instance as the owner and controller
	// if err := controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
	// 	return reconcile.Result{}, err
	// }

	if _, err := Cloudformation.CreateStack(input); err != nil {
		return err
	}

	if err := r.waitWhile(stack, cloudformation.StackStatusCreateInProgress); err != nil {
		return err
	}

	return r.updateStackStatus(stack)
}

func (r *ReconcileStack) updateStack(stack *cloudformationv1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("updating stack")

	if DryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack update")
		return nil
	}

	input := &cloudformation.UpdateStackInput{
		Capabilities: aws.StringSlice(Capabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   stackParameters(stack),
		Tags:         stackTags(stack, Tags),
	}

	if _, err := Cloudformation.UpdateStack(input); err != nil {
		if strings.Contains(err.Error(), "No updates are to be performed.") {
			logrus.WithField("stack", stack.Name).Debug("stack already updated")
			return nil
		}
		return err
	}

	if err := r.waitWhile(stack, cloudformation.StackStatusUpdateInProgress); err != nil {
		return err
	}

	return r.updateStackStatus(stack)
}

func (r *ReconcileStack) deleteStack(stack *cloudformationv1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("deleting stack")

	if DryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack deletion")
		return nil
	}

	logrus.WithField("stack", stack.Name).Info("skipping stack deletion")

	input := &cloudformation.DeleteStackInput{
		StackName: aws.String(stack.Name),
	}

	if _, err := Cloudformation.DeleteStack(input); err != nil {
		return err
	}

	return r.waitWhile(stack, cloudformation.StackStatusDeleteInProgress)
}

func (r *ReconcileStack) getStack(stack *cloudformationv1alpha1.Stack) (*cloudformation.Stack, error) {
	resp, err := Cloudformation.DescribeStacks(&cloudformation.DescribeStacksInput{
		StackName: aws.String(stack.Name),
	})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil, ErrStackNotFound
		}
		return nil, err
	}
	if len(resp.Stacks) != 1 {
		return nil, ErrStackNotFound
	}

	return resp.Stacks[0], nil
}

func (r *ReconcileStack) stackExists(stack *cloudformationv1alpha1.Stack) (bool, error) {
	_, err := r.getStack(stack)
	if err != nil {
		if err == ErrStackNotFound {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (r *ReconcileStack) hasOwnership(stack *cloudformationv1alpha1.Stack) (bool, error) {
	exists, err := r.stackExists(stack)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}

	cfs, err := r.getStack(stack)
	if err != nil {
		return false, err
	}

	for _, tag := range cfs.Tags {
		if aws.StringValue(tag.Key) == ownerTagKey && aws.StringValue(tag.Value) == ownerTagValue {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcileStack) updateStackStatus(stack *cloudformationv1alpha1.Stack) error {
	cfs, err := r.getStack(stack)
	if err != nil {
		return err
	}

	stackID := aws.StringValue(cfs.StackId)
	outputs := map[string]string{}
	for _, output := range cfs.Outputs {
		outputs[aws.StringValue(output.OutputKey)] = aws.StringValue(output.OutputValue)
	}

	if stackID != stack.Status.StackID || !reflect.DeepEqual(outputs, stack.Status.Outputs) {
		stack.Status.StackID = stackID
		stack.Status.Outputs = outputs

		err := r.client.Status().Update(context.TODO(), stack)
		if err != nil {
			if errors.IsNotFound(err) {
				// Request object not found, could have been deleted after reconcile request.
				// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
				// Return and don't requeue
				// return reconcile.Result{}, nil
				return nil
			}
			// Error reading the object - requeue the request.
			// return reconcile.Result{}, err
			return err
		}
	}

	return nil
}

func (r *ReconcileStack) waitWhile(stack *cloudformationv1alpha1.Stack, status string) error {
	for {
		cfs, err := r.getStack(stack)
		if err != nil {
			if err == ErrStackNotFound {
				return nil
			}
			return err
		}
		current := aws.StringValue(cfs.StackStatus)

		logrus.WithFields(logrus.Fields{
			"stack":  stack.Name,
			"status": current,
		}).Debug("waiting for stack")

		if current == status {
			time.Sleep(time.Second)
			continue
		}

		return nil
	}
}

// stackParameters converts the parameters field on a Stack resource to CloudFormation Parameters.
func stackParameters(stack *cloudformationv1alpha1.Stack) []*cloudformation.Parameter {
	params := []*cloudformation.Parameter{}
	for k, v := range stack.Spec.Parameters {
		params = append(params, &cloudformation.Parameter{
			ParameterKey:   aws.String(k),
			ParameterValue: aws.String(v),
		})
	}
	return params
}

// stackTags converts the tags field on a Stack resource to CloudFormation Tags.
// Furthermore, it adds a tag for marking ownership as well as any tags given by defaultTags.
func stackTags(stack *cloudformationv1alpha1.Stack, defaultTags map[string]string) []*cloudformation.Tag {
	// ownership tag
	tags := []*cloudformation.Tag{
		{
			Key:   aws.String(ownerTagKey),
			Value: aws.String(ownerTagValue),
		},
	}
	// default tags
	for k, v := range defaultTags {
		tags = append(tags, &cloudformation.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	// tags specified on the Stack resource
	for k, v := range stack.Spec.Tags {
		tags = append(tags, &cloudformation.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return tags
}
