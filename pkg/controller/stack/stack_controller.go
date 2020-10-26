package stack

import (
	"context"
	coreerrors "errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/jpillora/backoff"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/cloudformation/cloudformationiface"

	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/pkg/apis/cloudformation/v1alpha1"
)

const (
	controllerKey   = "kubernetes.io/controlled-by"
	controllerValue = "cloudformation.linki.space/operator"
	stacksFinalizer = "finalizer.cloudformation.linki.space"
	ownerKey        = "kubernetes.io/owned-by"
)

var (
	StackFlagSet     *pflag.FlagSet
	ErrStackNotFound = coreerrors.New("stack not found")
)

func init() {
	StackFlagSet = pflag.NewFlagSet("stack", pflag.ExitOnError)

	StackFlagSet.String("region", "eu-central-1", "The AWS region to use")
	StackFlagSet.String("assume-role", "", "Assume AWS role when defined. Useful for stacks in another AWS account. Specify the full ARN, e.g. `arn:aws:iam::123456789:role/cloudformation-operator`")
	StackFlagSet.StringToString("tag", map[string]string{}, "Tags to apply to all Stacks by default. Specify multiple times for multiple tags.")
	StackFlagSet.StringSlice("capability", []string{}, "The AWS CloudFormation capability to enable")
	StackFlagSet.Bool("dry-run", false, "If true, don't actually do anything.")
}

var log = logf.Log.WithName("controller_stack")

// Add creates a new Stack Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	assumeRole, err := StackFlagSet.GetString("assume-role")
	if err != nil {
		log.Error(err, "error parsing flag")
		os.Exit(1)
	}

	region, err := StackFlagSet.GetString("region")
	if err != nil {
		log.Error(err, "error parsing flag")
		os.Exit(1)
	}

	defaultTags, err := StackFlagSet.GetStringToString("tag")
	if err != nil {
		log.Error(err, "error parsing flag")
		os.Exit(1)
	}

	defaultCapabilities, err := StackFlagSet.GetStringSlice("capability")
	if err != nil {
		log.Error(err, "error parsing flag")
		os.Exit(1)
	}

	dryRun, err := StackFlagSet.GetBool("dry-run")
	if err != nil {
		log.Error(err, "error parsing flag")
		os.Exit(1)
	}

	var client cloudformationiface.CloudFormationAPI
	sess := session.Must(session.NewSession())
	log.Info(assumeRole)
	if assumeRole != "" {
		log.Info("run assume")
		creds := stscreds.NewCredentials(sess, assumeRole)
		client = cloudformation.New(sess, &aws.Config{
			Credentials: creds,
			Region:      aws.String(region),
		})
	} else {
		client = cloudformation.New(sess, &aws.Config{
			Region: aws.String(region),
		})
	}

	return &ReconcileStack{
		client:              mgr.GetClient(),
		scheme:              mgr.GetScheme(),
		cf:                  client,
		defaultTags:         defaultTags,
		defaultCapabilities: defaultCapabilities,
		dryRun:              dryRun,
	}
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
	client              client.Client
	scheme              *runtime.Scheme
	cf                  cloudformationiface.CloudFormationAPI
	defaultTags         map[string]string
	defaultCapabilities []string
	dryRun              bool
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
			reqLogger.Info("Stack resource not found. Ignoring since object must be deleted")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get Stack")
		return reconcile.Result{}, err
	}

	// Check if the Stack instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isStackMarkedToBeDeleted := instance.GetDeletionTimestamp() != nil
	if isStackMarkedToBeDeleted {
		if contains(instance.GetFinalizers(), stacksFinalizer) {
			// Run finalization logic for stacksFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeStacks(reqLogger, instance); err != nil {
				return reconcile.Result{}, err
			}

			// Remove stacksFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			instance.SetFinalizers(remove(instance.GetFinalizers(), stacksFinalizer))
			err := r.client.Update(context.TODO(), instance)
			if err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(instance.GetFinalizers(), stacksFinalizer) {
		if err := r.addFinalizer(reqLogger, instance); err != nil {
			return reconcile.Result{}, err
		}
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

	if r.dryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack creation")
		return nil
	}

	hasOwnership, err := r.hasOwnership(stack)
	if err != nil {
		return err
	}

	if !hasOwnership {
		logrus.WithField("stack", stack.Name).Info("no ownerhsip")
		return nil
	}

	stackTags, err := r.stackTags(stack)
	if err != nil {
		logrus.WithField("stack", stack.Name).Error("error compiling tags")
		return err
	}

	input := &cloudformation.CreateStackInput{
		Capabilities: aws.StringSlice(r.defaultCapabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   r.stackParameters(stack),
		Tags:         stackTags,
	}

	if _, err := r.cf.CreateStack(input); err != nil {
		return err
	}

	if err := r.waitWhile(stack, cloudformation.StackStatusCreateInProgress); err != nil {
		return err
	}

	return r.updateStackStatus(stack)
}

func (r *ReconcileStack) updateStack(stack *cloudformationv1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("updating stack")

	if r.dryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack update")
		return nil
	}

	hasOwnership, err := r.hasOwnership(stack)
	if err != nil {
		return err
	}

	if !hasOwnership {
		logrus.WithField("stack", stack.Name).Info("no ownership")
		return nil
	}

	stackTags, err := r.stackTags(stack)
	if err != nil {
		logrus.WithField("stack", stack.Name).Error("error compiling tags")
		return err
	}

	// Skip update if the stack remains in CREATE_IN_PROGRESS
	createInProgress, err := r.checkStackStatus(stack, cloudformation.StackStatusCreateInProgress)
	if err != nil {
		return err
	} else if createInProgress {
		logrus.WithField("stack", stack.Name).Info("update skipped, stack status 'Create In Progress'")
		return nil
	}

	input := &cloudformation.UpdateStackInput{
		Capabilities: aws.StringSlice(r.defaultCapabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   r.stackParameters(stack),
		Tags:         stackTags,
	}

	if _, err := r.cf.UpdateStack(input); err != nil {
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

	if r.dryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack deletion")
		return nil
	}

	hasOwnership, err := r.hasOwnership(stack)
	if err != nil {
		return err
	}

	if !hasOwnership {
		logrus.WithField("stack", stack.Name).Info("no ownerhsip")
		return nil
	}

	input := &cloudformation.DeleteStackInput{
		StackName: aws.String(stack.Name),
	}

	if _, err := r.cf.DeleteStack(input); err != nil {
		return err
	}

	return r.waitWhile(stack, cloudformation.StackStatusDeleteInProgress)
}

func (r *ReconcileStack) getStack(stack *cloudformationv1alpha1.Stack) (*cloudformation.Stack, error) {
	b := &backoff.Backoff{
		Min:    1 * time.Second,
		Max:    2 * time.Minute,
		Factor: 3,
		Jitter: true, // Use jitter as many of these requests happen
	}

	for {
		resp, err := r.cf.DescribeStacks(&cloudformation.DescribeStacksInput{
			StackName: aws.String(stack.Name),
		})
		if err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				return nil, ErrStackNotFound
			}
			if strings.Contains(err.Error(), "Rate exceeded") {
				logrus.WithField("stack", stack.Name).Error("Rate limited by AWS, sleep and retry initiated")
				time.Sleep(b.Duration()) // Throttled by AWS, sleep using backoff duration
				continue
			}
			return nil, err
		}
		if len(resp.Stacks) != 1 {
			return nil, ErrStackNotFound
		}

		return resp.Stacks[0], nil
	}
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

func (r *ReconcileStack) checkStackStatus(stack *cloudformationv1alpha1.Stack, desiredStatus string) (bool, error) {
	cfs, err := r.getStack(stack)
	if err != nil {
		if err == ErrStackNotFound {
			return false, nil
		}
		return false, err
	}
	currentStatus := aws.StringValue(cfs.StackStatus)

	logrus.WithFields(logrus.Fields{
		"stack":  stack.Name,
		"status": currentStatus,
	}).Debug("checking stack status")

	if currentStatus == desiredStatus {
		return true, nil
	}

	return false, nil
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
		if aws.StringValue(tag.Key) == controllerKey && aws.StringValue(tag.Value) == controllerValue {
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
	b := &backoff.Backoff{
		Min:    1 * time.Second,
		Max:    1 * time.Minute,
		Factor: 3,
		Jitter: true, // Use jitter as many of these requests happen
	}

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
			time.Sleep(b.Duration()) // Sleep using backoff duration
			continue
		}

		return nil
	}
}

// stackParameters converts the parameters field on a Stack resource to CloudFormation Parameters.
func (r *ReconcileStack) stackParameters(stack *cloudformationv1alpha1.Stack) []*cloudformation.Parameter {
	params := []*cloudformation.Parameter{}
	for k, v := range stack.Spec.Parameters {
		params = append(params, &cloudformation.Parameter{
			ParameterKey:   aws.String(k),
			ParameterValue: aws.String(v),
		})
	}
	return params
}

func (r *ReconcileStack) getObjectReference(owner metav1.Object) (types.UID, error) {
	ro, ok := owner.(runtime.Object)
	if !ok {
		return "", fmt.Errorf("%T is not a runtime.Object, cannot call SetControllerReference", owner)
	}

	gvk, err := apiutil.GVKForObject(ro, r.scheme)
	if err != nil {
		return "", err
	}

	ref := *metav1.NewControllerRef(owner, schema.GroupVersionKind{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind})
	return ref.UID, nil
}

// stackTags converts the tags field on a Stack resource to CloudFormation Tags.
// Furthermore, it adds a tag for marking ownership as well as any tags given by defaultTags.
func (r *ReconcileStack) stackTags(stack *cloudformationv1alpha1.Stack) ([]*cloudformation.Tag, error) {
	ref, err := r.getObjectReference(stack)
	if err != nil {
		return nil, err
	}

	// ownership tags
	tags := []*cloudformation.Tag{
		{
			Key:   aws.String(controllerKey),
			Value: aws.String(controllerValue),
		},
		{
			Key:   aws.String(ownerKey),
			Value: aws.String(string(ref)),
		},
	}

	// default tags
	for k, v := range r.defaultTags {
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

	return tags, nil
}

func (r *ReconcileStack) finalizeStacks(reqLogger logr.Logger, stack *cloudformationv1alpha1.Stack) error {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.
	if err := r.deleteStack(stack); err != nil {
		return err
	}

	reqLogger.Info("Successfully finalized stacks")
	return nil
}

func (r *ReconcileStack) addFinalizer(reqLogger logr.Logger, m *cloudformationv1alpha1.Stack) error {
	reqLogger.Info("Adding Finalizer for the Stack")
	m.SetFinalizers(append(m.GetFinalizers(), stacksFinalizer))

	// Update CR
	err := r.client.Update(context.TODO(), m)
	if err != nil {
		reqLogger.Error(err, "Failed to update Stack with finalizer")
		return err
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}
