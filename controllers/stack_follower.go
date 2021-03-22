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
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/go-logr/logr"
	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sync"
	"time"
)

// StackFollower ensures a Stack object is monitored until it reaches a terminal state
type StackFollower struct {
	client.Client
	Log                  logr.Logger
	CloudFormationHelper *CloudFormationHelper
	SubmissionChannel    chan *cloudformationv1alpha1.Stack
	// StackID -> Kube Stack object
	mapPollingList sync.Map
}

func (f *StackFollower) Receiver() {

	for {
		toBeFollowed := <-f.SubmissionChannel
		f.Log.Info("Received follow request", "UID", toBeFollowed.UID, "Stack ID", toBeFollowed.Status.StackID)
		if !f.BeingFollowed(toBeFollowed.Status.StackID) {
			f.startFollowing(toBeFollowed)
		}
		_ = f.UpdateStackStatus(context.TODO(), toBeFollowed)
	}
}

// Identify if the follower is actively working this one.
func (f *StackFollower) BeingFollowed(stackId string) bool {
	_, followed := f.mapPollingList.Load(stackId)
	f.Log.Info("Following Stack", "StackID", stackId, "Following", followed)
	return followed
}

// Identify if the follower is actively working this one.
func (f *StackFollower) startFollowing(stack *cloudformationv1alpha1.Stack) {
	f.mapPollingList.Store(stack.Status.StackID, stack)
	f.Log.Info("Now following Stack", "StackID", stack.Status.StackID)
}

// Identify if the follower is actively working this one.
func (f *StackFollower) stopFollowing(stackId string) {
	f.mapPollingList.Delete(stackId)
	f.Log.Info("Stopped following Stack", "StackID", stackId)
}

// Allow passing a current/recent fetch of the stack object to the method (optionally)
func (f *StackFollower) UpdateStackStatus(ctx context.Context, instance *cloudformationv1alpha1.Stack, stack ...*cfTypes.Stack) error {
	var err error
	var cfs *cfTypes.Stack
	update := false

	if len(stack) > 0 {
		cfs = stack[0]
	}
	if cfs == nil {
		cfs, err = f.CloudFormationHelper.GetStack(ctx, instance)
		if err != nil {
			f.Log.Error(err, "Failed to get CloudFormation stack")
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
	if string(cfs.StackStatus) != instance.Status.StackStatus {
		update = true
		instance.Status.StackStatus = string(cfs.StackStatus)
		instance.Status.CreatedTime = metav1.NewTime(*cfs.CreationTime)
		if cfs.LastUpdatedTime != nil {
			instance.Status.UpdatedTime = metav1.NewTime(*cfs.LastUpdatedTime)
		}
	}

	// Checking stack ID and outputs for changes.
	stackID := *cfs.StackId
	if stackID != instance.Status.StackID || !reflect.DeepEqual(outputs, instance.Status.Outputs) {
		update = true
		instance.Status.StackID = stackID
		if len(outputs) > 0 {
			instance.Status.Outputs = outputs
		}
	}

	// Recording all stack resources
	resources, err := f.CloudFormationHelper.GetStackResources(ctx, instance.Status.StackID)
	if err != nil {
		f.Log.Error(err, "Failed to get Stack Resources")
		return err
	}
	if !reflect.DeepEqual(resources, instance.Status.Resources) {
		update = true
		instance.Status.Resources = resources
	}

	if update {
		err = f.Status().Update(ctx, instance)
		if err != nil {
			f.Log.Error(err, "Failed to update Stack Status")
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

func (f *StackFollower) processStack(key interface{}, value interface{}) bool {

	stackId := key.(string)
	stack := value.(*cloudformationv1alpha1.Stack)

	cfs, err := f.CloudFormationHelper.GetStack(context.TODO(), stack)
	if err != nil {
		if err == ErrStackNotFound {
			f.Log.Error(err, "Stack Not Found", "UID", stack.UID, "Stack ID", stackId)
			f.stopFollowing(stackId)
		} else {
			f.Log.Error(err, "Error retrieving stack for processing", "UID", stack.UID, "Stack ID", stackId)
		}
	} else {
		// Have to remove the lock on the last pass, so the reconciler can catch it on the next loop.
		if f.CloudFormationHelper.StackInTerminalState(cfs.StackStatus) {
			f.stopFollowing(stackId)
		}
		err = f.UpdateStackStatus(context.TODO(), stack, cfs)
		if err != nil {
			f.Log.Error(err, "Failed to update stack status", "UID", stack.UID, "Stack ID", stackId)
			// On error put it back to make sure we save it next time.
			f.startFollowing(stack)
		}
	}

	return true
}

func (f *StackFollower) Worker() {

	for {
		time.Sleep(time.Second * 5)
		f.mapPollingList.Range(f.processStack)
	}

}
