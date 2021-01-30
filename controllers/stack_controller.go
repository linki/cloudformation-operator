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

package controllers

import (
	"context"
	coreerrors "errors"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/cloudformation/cloudformationiface"

	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/api/v1alpha1"
)

const (
	controllerKey   = "kubernetes.io/controlled-by"
	controllerValue = "cloudformation.linki.space/operator"
	stacksFinalizer = "finalizer.cloudformation.linki.space"
	ownerKey        = "kubernetes.io/owned-by"
)

var (
	ErrStackNotFound = coreerrors.New("stack not found")
)

// StackReconciler reconciles a Stack object
type StackReconciler struct {
	client.Client
	Log                 logr.Logger
	Scheme              *runtime.Scheme
	CloudFormation      cloudformationiface.CloudFormationAPI
	DefaultTags         map[string]string
	DefaultCapabilities []string
	DryRun              bool
}

// +kubebuilder:rbac:groups=cloudformation.linki.space,resources=stacks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudformation.linki.space,resources=stacks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudformation.linki.space,resources=stacks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Stack object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *StackReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)

	// your logic here
	// Fetch the Stack instance
	instance := &cloudformationv1alpha1.Stack{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			r.Log.Info("Stack resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		r.Log.Error(err, "Failed to get Stack")
		return ctrl.Result{}, err
	}

	// Check if the Stack instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isStackMarkedToBeDeleted := instance.GetDeletionTimestamp() != nil
	if isStackMarkedToBeDeleted {
		if contains(instance.GetFinalizers(), stacksFinalizer) {
			// Run finalization logic for stacksFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeStacks(r.Log, instance); err != nil {
				return ctrl.Result{}, err
			}

			// Remove stacksFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(instance, stacksFinalizer)
			err := r.Update(context.TODO(), instance)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(instance.GetFinalizers(), stacksFinalizer) {
		controllerutil.AddFinalizer(instance, stacksFinalizer)
		r.Update(context.TODO(), instance)
	}

	exists, err := r.stackExists(instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	if exists {
		return reconcile.Result{}, r.updateStack(instance)
	}

	return ctrl.Result{}, r.createStack(instance)
}

func (r *StackReconciler) createStack(stack *cloudformationv1alpha1.Stack) error {
	r.Log.WithValues("stack", stack.Name).Info("creating stack")

	if r.DryRun {
		r.Log.WithValues("stack", stack.Name).Info("skipping stack creation")
		return nil
	}

	hasOwnership, err := r.hasOwnership(stack)
	if err != nil {
		return err
	}

	if !hasOwnership {
		r.Log.WithValues("stack", stack.Name).Info("no ownerhsip")
		return nil
	}

	stackTags, err := r.stackTags(stack)
	if err != nil {
		r.Log.WithValues("stack", stack.Name).Error(err, "error compiling tags")
		return err
	}

	input := &cloudformation.CreateStackInput{
		Capabilities: aws.StringSlice(r.DefaultCapabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   r.stackParameters(stack),
		Tags:         stackTags,
	}

	if _, err := r.CloudFormation.CreateStack(input); err != nil {
		return err
	}

	if err := r.waitWhile(stack, cloudformation.StackStatusCreateInProgress); err != nil {
		return err
	}

	return r.updateStackStatus(stack)
}

func (r *StackReconciler) updateStack(stack *cloudformationv1alpha1.Stack) error {
	r.Log.WithValues("stack", stack.Name).Info("updating stack")

	if r.DryRun {
		r.Log.WithValues("stack", stack.Name).Info("skipping stack update")
		return nil
	}

	hasOwnership, err := r.hasOwnership(stack)
	if err != nil {
		return err
	}

	if !hasOwnership {
		r.Log.WithValues("stack", stack.Name).Info("no ownerhsip")
		return nil
	}

	stackTags, err := r.stackTags(stack)
	if err != nil {
		r.Log.WithValues("stack", stack.Name).Error(err, "error compiling tags")
		return err
	}

	input := &cloudformation.UpdateStackInput{
		Capabilities: aws.StringSlice(r.DefaultCapabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   r.stackParameters(stack),
		Tags:         stackTags,
	}

	if _, err := r.CloudFormation.UpdateStack(input); err != nil {
		if strings.Contains(err.Error(), "No updates are to be performed.") {
			r.Log.WithValues("stack", stack.Name).Info("stack already updated")
			return nil
		}
		return err
	}

	if err := r.waitWhile(stack, cloudformation.StackStatusUpdateInProgress); err != nil {
		return err
	}

	return r.updateStackStatus(stack)
}

func (r *StackReconciler) deleteStack(stack *cloudformationv1alpha1.Stack) error {
	r.Log.WithValues("stack", stack.Name).Info("deleting stack")

	if r.DryRun {
		r.Log.WithValues("stack", stack.Name).Info("skipping stack deletion")
		return nil
	}

	hasOwnership, err := r.hasOwnership(stack)
	if err != nil {
		return err
	}

	if !hasOwnership {
		r.Log.WithValues("stack", stack.Name).Info("no ownerhsip")
		return nil
	}

	input := &cloudformation.DeleteStackInput{
		StackName: aws.String(stack.Name),
	}

	if _, err := r.CloudFormation.DeleteStack(input); err != nil {
		return err
	}

	return r.waitWhile(stack, cloudformation.StackStatusDeleteInProgress)
}

func (r *StackReconciler) getStack(stack *cloudformationv1alpha1.Stack) (*cloudformation.Stack, error) {
	resp, err := r.CloudFormation.DescribeStacks(&cloudformation.DescribeStacksInput{
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

func (r *StackReconciler) stackExists(stack *cloudformationv1alpha1.Stack) (bool, error) {
	_, err := r.getStack(stack)
	if err != nil {
		if err == ErrStackNotFound {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (r *StackReconciler) hasOwnership(stack *cloudformationv1alpha1.Stack) (bool, error) {
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

func (r *StackReconciler) updateStackStatus(stack *cloudformationv1alpha1.Stack) error {
	cfs, err := r.getStack(stack)
	if err != nil {
		return err
	}

	stackID := aws.StringValue(cfs.StackId)

	outputs := map[string]string{}
	if cfs.Outputs != nil && len(cfs.Outputs) > 0 {
		for _, output := range cfs.Outputs {
			outputs[aws.StringValue(output.OutputKey)] = aws.StringValue(output.OutputValue)
		}
	}

	if stackID != stack.Status.StackID || !reflect.DeepEqual(outputs, stack.Status.Outputs) {
		stack.Status.StackID = stackID
		if len(outputs) > 0 {
			stack.Status.Outputs = outputs
		}

		err := r.Status().Update(context.TODO(), stack)
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

func (r *StackReconciler) waitWhile(stack *cloudformationv1alpha1.Stack, status string) error {
	for {
		cfs, err := r.getStack(stack)
		if err != nil {
			if err == ErrStackNotFound {
				return nil
			}
			return err
		}
		current := aws.StringValue(cfs.StackStatus)

		r.Log.WithValues("stack", stack.Name, "status", current).Info("waiting for stack")

		if current == status {
			time.Sleep(time.Second)
			continue
		}

		return nil
	}
}

// stackParameters converts the parameters field on a Stack resource to CloudFormation Parameters.
func (r *StackReconciler) stackParameters(stack *cloudformationv1alpha1.Stack) []*cloudformation.Parameter {
	var params []*cloudformation.Parameter
	if stack.Spec.Parameters != nil {
		for k, v := range stack.Spec.Parameters {
			params = append(params, &cloudformation.Parameter{
				ParameterKey:   aws.String(k),
				ParameterValue: aws.String(v),
			})
		}
	}
	return params
}

func (r *StackReconciler) getObjectReference(owner metav1.Object) (types.UID, error) {
	ro, ok := owner.(runtime.Object)
	if !ok {
		return "", fmt.Errorf("%T is not a runtime.Object, cannot call SetControllerReference", owner)
	}

	gvk, err := apiutil.GVKForObject(ro, r.Scheme)
	if err != nil {
		return "", err
	}

	ref := *metav1.NewControllerRef(owner, schema.GroupVersionKind{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind})
	return ref.UID, nil
}

// stackTags converts the tags field on a Stack resource to CloudFormation Tags.
// Furthermore, it adds a tag for marking ownership as well as any tags given by defaultTags.
func (r *StackReconciler) stackTags(stack *cloudformationv1alpha1.Stack) ([]*cloudformation.Tag, error) {
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
	for k, v := range r.DefaultTags {
		tags = append(tags, &cloudformation.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	// tags specified on the Stack resource
	if stack.Spec.Tags != nil {
		for k, v := range stack.Spec.Tags {
			tags = append(tags, &cloudformation.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			})
		}
	}

	return tags, nil
}

func (r *StackReconciler) finalizeStacks(reqLogger logr.Logger, stack *cloudformationv1alpha1.Stack) error {
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *StackReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudformationv1alpha1.Stack{}).
		Complete(r)
}
