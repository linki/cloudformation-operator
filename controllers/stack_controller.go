/*
MIT License

Copyright (c) 2018 Martin Linkhorst
Copyright (c) 2021 Stephen Cuppett

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cf_types "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"

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
	CloudFormation      *cloudformation.Client
	DefaultTags         map[string]string
	DefaultCapabilities []cf_types.Capability
	DryRun              bool
}

type StackLoop struct {
	ctx      context.Context
	req      ctrl.Request
	instance *cloudformationv1alpha1.Stack
}

// +kubebuilder:rbac:groups=cloudformation.linki.space,resources=stacks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudformation.linki.space,resources=stacks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudformation.linki.space,resources=stacks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *StackReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)

	loop := &StackLoop{ctx, req, &cloudformationv1alpha1.Stack{}}

	// Fetch the Stack instance
	err := r.Client.Get(ctx, req.NamespacedName, loop.instance)
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
	isStackMarkedToBeDeleted := loop.instance.GetDeletionTimestamp() != nil
	if isStackMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(loop.instance, stacksFinalizer) {
			// Run finalization logic for stacksFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeStacks(loop); err != nil {
				return ctrl.Result{}, err
			}

			// Remove stacksFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(loop.instance, stacksFinalizer)
			err := r.Update(ctx, loop.instance)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !controllerutil.ContainsFinalizer(loop.instance, stacksFinalizer) {
		controllerutil.AddFinalizer(loop.instance, stacksFinalizer)
		r.Update(ctx, loop.instance)
		return ctrl.Result{}, nil
	}

	exists, err := r.stackExists(loop)
	if err != nil {
		return reconcile.Result{}, err
	}

	if exists {
		return reconcile.Result{}, r.updateStack(loop)
	}

	return ctrl.Result{}, r.createStack(loop)
}

func (r *StackReconciler) createStack(loop *StackLoop) error {
	r.Log.WithValues("stack", loop.instance.Name).Info("creating stack")

	if r.DryRun {
		r.Log.WithValues("stack", loop.instance.Name).Info("skipping stack creation")
		return nil
	}

	hasOwnership, err := r.hasOwnership(loop)
	if err != nil {
		return err
	}

	if !hasOwnership {
		r.Log.WithValues("stack", loop.instance.Name).Info("no ownership")
		return nil
	}

	stackTags, err := r.stackTags(loop)
	if err != nil {
		r.Log.WithValues("stack", loop.instance.Name).Error(err, "error compiling tags")
		return err
	}

	input := &cloudformation.CreateStackInput{
		Capabilities: r.DefaultCapabilities,
		StackName:    aws.String(loop.instance.Name),
		TemplateBody: aws.String(loop.instance.Spec.Template),
		Parameters:   r.stackParameters(loop),
		Tags:         stackTags,
	}

	if _, err := r.CloudFormation.CreateStack(loop.ctx, input); err != nil {
		return err
	}

	if err := r.waitTillDone(loop); err != nil {
		return err
	}

	return r.updateStackStatus(loop)
}

func (r *StackReconciler) updateStack(loop *StackLoop) error {
	r.Log.WithValues("stack", loop.instance.Name).Info("updating stack")

	if r.DryRun {
		r.Log.WithValues("stack", loop.instance.Name).Info("skipping stack update")
		return nil
	}

	hasOwnership, err := r.hasOwnership(loop)
	if err != nil {
		return err
	}

	if !hasOwnership {
		r.Log.WithValues("stack", loop.instance.Name).Info("no ownership")
		return nil
	}

	stackTags, err := r.stackTags(loop)
	if err != nil {
		r.Log.WithValues("stack", loop.instance.Name).Error(err, "error compiling tags")
		return err
	}

	input := &cloudformation.UpdateStackInput{
		Capabilities: r.DefaultCapabilities,
		StackName:    aws.String(loop.instance.Name),
		TemplateBody: aws.String(loop.instance.Spec.Template),
		Parameters:   r.stackParameters(loop),
		Tags:         stackTags,
	}

	if _, err := r.CloudFormation.UpdateStack(loop.ctx, input); err != nil {
		if strings.Contains(err.Error(), "No updates are to be performed.") {
			r.Log.WithValues("stack", loop.instance.Name).Info("stack already updated")
			return nil
		}
		return err
	}

	if err := r.waitTillDone(loop); err != nil {
		return err
	}

	return r.updateStackStatus(loop)
}

func (r *StackReconciler) deleteStack(loop *StackLoop) error {
	r.Log.WithValues("stack", loop.instance.Name).Info("deleting stack")

	if r.DryRun {
		r.Log.WithValues("stack", loop.instance.Name).Info("skipping stack deletion")
		return nil
	}

	hasOwnership, err := r.hasOwnership(loop)
	if err != nil {
		return err
	}

	if !hasOwnership {
		r.Log.WithValues("stack", loop.instance.Name).Info("no ownership")
		return nil
	}

	input := &cloudformation.DeleteStackInput{
		StackName: aws.String(loop.instance.Name),
	}

	if _, err := r.CloudFormation.DeleteStack(loop.ctx, input); err != nil {
		return err
	}

	return r.waitTillDone(loop)
}

func (r *StackReconciler) getStack(loop *StackLoop) (*cf_types.Stack, error) {
	resp, err := r.CloudFormation.DescribeStacks(loop.ctx, &cloudformation.DescribeStacksInput{
		NextToken: nil,
		StackName: aws.String(loop.instance.Name),
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

	return &resp.Stacks[0], nil
}

func (r *StackReconciler) getStackResources(loop *StackLoop) ([]cloudformationv1alpha1.StackResource, error) {

	var next *string
	next = nil
	toReturn := make([]cloudformationv1alpha1.StackResource, 0)

	for {
		resp, err := r.CloudFormation.ListStackResources(loop.ctx, &cloudformation.ListStackResourcesInput{
			NextToken: next,
			StackName: aws.String(loop.instance.Name),
		})
		if err != nil {
			return nil, err
		}

		for _, e := range resp.StackResourceSummaries {
			reason := ""
			if e.ResourceStatusReason != nil {
				reason = *e.ResourceStatusReason
			}
			resourceSummary := cloudformationv1alpha1.StackResource{
				LogicalId:    *e.LogicalResourceId,
				PhysicalId:   *e.PhysicalResourceId,
				Type:         *e.ResourceType,
				Status:       string(e.ResourceStatus),
				StatusReason: reason,
			}
			toReturn = append(toReturn, resourceSummary)
		}

		next = resp.NextToken
		if next == nil {
			break
		}
	}

	return toReturn, nil
}

func (r *StackReconciler) stackExists(loop *StackLoop) (bool, error) {
	_, err := r.getStack(loop)
	if err != nil {
		if err == ErrStackNotFound {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (r *StackReconciler) hasOwnership(loop *StackLoop) (bool, error) {
	exists, err := r.stackExists(loop)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}

	cfs, err := r.getStack(loop)
	if err != nil {
		return false, err
	}

	for _, tag := range cfs.Tags {
		if *tag.Key == controllerKey && *tag.Value == controllerValue {
			return true, nil
		}
	}

	return false, nil
}

// Allow passing a current/recent fetch of the stack object to the method (optionally)
func (r *StackReconciler) updateStackStatus(loop *StackLoop, stack ...*cf_types.Stack) error {
	var err error
	var cfs *cf_types.Stack
	update := false

	if len(stack) > 0 {
		cfs = stack[0]
	}
	if cfs == nil {
		cfs, err = r.getStack(loop)
		if err != nil {
			return err
		}
	}

	outputs := map[string]string{}
	if cfs.Outputs != nil && len(cfs.Outputs) > 0 {
		for _, output := range cfs.Outputs {
			outputs[*output.OutputKey] = *output.OutputValue
		}
	}

	// Checking the status
	if string(cfs.StackStatus) != loop.instance.Status.StackStatus {
		update = true
		loop.instance.Status.StackStatus = string(cfs.StackStatus)
		loop.instance.Status.CreatedTime = metav1.NewTime(*cfs.CreationTime)
		if cfs.LastUpdatedTime != nil {
			loop.instance.Status.UpdatedTime = metav1.NewTime(*cfs.LastUpdatedTime)
		}
	}

	// Checking stack ID and outputs for changes.
	stackID := *cfs.StackId
	if stackID != loop.instance.Status.StackID || !reflect.DeepEqual(outputs, loop.instance.Status.Outputs) {
		update = true
		loop.instance.Status.StackID = stackID
		if len(outputs) > 0 {
			loop.instance.Status.Outputs = outputs
		}
	}

	// Recording all stack resources
	resources, err := r.getStackResources(loop)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(resources, loop.instance.Status.Resources) {
		update = true
		loop.instance.Status.Resources = resources
	}

	if update {
		err = r.Status().Update(loop.ctx, loop.instance)
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

func (r *StackReconciler) waitTillDone(loop *StackLoop) error {
	terminalStates := []cf_types.StackStatus{
		cf_types.StackStatusCreateComplete,
		cf_types.StackStatusCreateFailed,
		cf_types.StackStatusDeleteComplete,
		cf_types.StackStatusDeleteFailed,
		cf_types.StackStatusRollbackComplete,
		cf_types.StackStatusRollbackFailed,
		cf_types.StackStatusUpdateComplete,
		cf_types.StackStatusUpdateRollbackComplete,
		cf_types.StackStatusUpdateRollbackFailed,
	}
	for {
		cfs, err := r.getStack(loop)
		if err != nil {
			if err == ErrStackNotFound {
				return nil
			}
			return err
		}
		current := cfs.StackStatus

		r.Log.WithValues("stack", loop.instance.Name, "status", current).Info("waiting for stack")

		if !contains(terminalStates, current) {
			// Let's update the status in Kubernetes as the stack transitions.
			if string(current) != loop.instance.Status.StackStatus {
				_ = r.updateStackStatus(loop, cfs)
			}
			time.Sleep(time.Second)
			continue
		}

		return nil
	}
}

func contains(s []cf_types.StackStatus, e cf_types.StackStatus) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// stackParameters converts the parameters field on a Stack resource to CloudFormation Parameters.
func (r *StackReconciler) stackParameters(loop *StackLoop) []cf_types.Parameter {
	var params []cf_types.Parameter
	if loop.instance.Spec.Parameters != nil {
		for k, v := range loop.instance.Spec.Parameters {
			params = append(params, cf_types.Parameter{
				ParameterKey:   aws.String(k),
				ParameterValue: aws.String(v),
			})
		}
	}
	return params
}

// stackTags converts the tags field on a Stack resource to CloudFormation Tags.
// Furthermore, it adds a tag for marking ownership as well as any tags given by defaultTags.
func (r *StackReconciler) stackTags(loop *StackLoop) ([]cf_types.Tag, error) {
	// ownership tags
	tags := []cf_types.Tag{
		{
			Key:   aws.String(controllerKey),
			Value: aws.String(controllerValue),
		},
		{
			Key:   aws.String(ownerKey),
			Value: aws.String(string(loop.instance.UID)),
		},
	}

	// default tags
	for k, v := range r.DefaultTags {
		tags = append(tags, cf_types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	// tags specified on the Stack resource
	if loop.instance.Spec.Tags != nil {
		for k, v := range loop.instance.Spec.Tags {
			tags = append(tags, cf_types.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			})
		}
	}

	return tags, nil
}

// Removing CloudFormation stack from AWS
func (r *StackReconciler) finalizeStacks(loop *StackLoop) error {
	if err := r.deleteStack(loop); err != nil {
		return err
	}

	r.Log.Info("Successfully finalized stacks")
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StackReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudformationv1alpha1.Stack{}).
		Complete(r)
}
