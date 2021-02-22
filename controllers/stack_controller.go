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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"

	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/api/v1alpha1"
)

const (
	controllerKey   = "kubernetes.io/controlled-by"
	controllerValue = "cloudformation.linki.space/operator"
	stacksFinalizer = "finalizer.cloudformation.linki.space"
	ownerKey        = "kubernetes.io/owned-by"
)

// StackReconciler reconciles a Stack object
type StackReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	CloudFormation       *cloudformation.Client
	StackFollower        *StackFollower
	CloudFormationHelper *CloudFormationHelper
	DefaultTags          map[string]string
	DefaultCapabilities  []cfTypes.Capability
	DryRun               bool
}

type StackLoop struct {
	ctx      context.Context
	req      ctrl.Request
	instance *cloudformationv1alpha1.Stack
	stack    *cfTypes.Stack
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

	loop := &StackLoop{ctx, req, &cloudformationv1alpha1.Stack{}, nil}

	// Fetch the Stack instance
	err := r.Client.Get(loop.ctx, loop.req.NamespacedName, loop.instance)
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
			// Remove stacksFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			if loop.instance.Status.StackStatus == "DELETE_COMPLETE" {
				controllerutil.RemoveFinalizer(loop.instance, stacksFinalizer)
				err := r.Update(loop.ctx, loop.instance)
				if err != nil {
					r.Log.Error(err, "Failed to update stack to drop finalizer")
					return ctrl.Result{}, err
				}
				r.Log.Info("Successfully finalized stack")
			} else {
				// Run finalization logic for stacksFinalizer. If the
				// finalization logic fails, don't remove the finalizer so
				// that we can retry during the next reconciliation.
				err := r.deleteStack(loop)
				if err != nil {
					r.Log.Error(err, "Failed to delete stack")
					return ctrl.Result{}, err
				}
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !controllerutil.ContainsFinalizer(loop.instance, stacksFinalizer) {
		controllerutil.AddFinalizer(loop.instance, stacksFinalizer)
		err = r.Update(ctx, loop.instance)
		return ctrl.Result{}, err
	}

	exists, err := r.stackExists(loop)
	if err != nil {
		return reconcile.Result{}, err
	}

	if exists {
		// If the stack is in progress but not being followed, follow it to catch updates
		// If it is being followed, we want the same thing, just send it over to the other thread to check it in all
		// IN_PROGRESS cases.
		if !r.CloudFormationHelper.StackInTerminalState(loop.stack.StackStatus) {
			r.StackFollower.SubmissionChannel <- loop.instance
			return ctrl.Result{}, nil
		}

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

	output, err := r.CloudFormation.CreateStack(loop.ctx, input)
	if err != nil {
		return err
	}
	loop.instance.Status.StackID = *output.StackId

	r.StackFollower.SubmissionChannel <- loop.instance
	return nil
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

	r.StackFollower.SubmissionChannel <- loop.instance
	return nil
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

	r.StackFollower.SubmissionChannel <- loop.instance
	return nil
}

func (r *StackReconciler) getStack(loop *StackLoop, noCache bool) (*cfTypes.Stack, error) {

	var err error

	if noCache || loop.stack == nil {
		// Must use the stack ID to get details/finalization for deleted stacks
		loop.stack, err = r.CloudFormationHelper.GetStack(loop.ctx, loop.instance)
		if err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				return nil, ErrStackNotFound
			}
			return nil, err
		}
	}

	return loop.stack, nil
}

func (r *StackReconciler) stackExists(loop *StackLoop) (bool, error) {
	_, err := r.getStack(loop, false)
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

	cfs, err := r.getStack(loop, false)
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

// stackParameters converts the parameters field on a Stack resource to CloudFormation Parameters.
func (r *StackReconciler) stackParameters(loop *StackLoop) []cfTypes.Parameter {
	var params []cfTypes.Parameter
	if loop.instance.Spec.Parameters != nil {
		for k, v := range loop.instance.Spec.Parameters {
			params = append(params, cfTypes.Parameter{
				ParameterKey:   aws.String(k),
				ParameterValue: aws.String(v),
			})
		}
	}
	return params
}

// stackTags converts the tags field on a Stack resource to CloudFormation Tags.
// Furthermore, it adds a tag for marking ownership as well as any tags given by defaultTags.
func (r *StackReconciler) stackTags(loop *StackLoop) ([]cfTypes.Tag, error) {
	// ownership tags
	tags := []cfTypes.Tag{
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
		tags = append(tags, cfTypes.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	// tags specified on the Stack resource
	if loop.instance.Spec.Tags != nil {
		for k, v := range loop.instance.Spec.Tags {
			tags = append(tags, cfTypes.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			})
		}
	}

	return tags, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StackReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudformationv1alpha1.Stack{}).
		Complete(r)
}
