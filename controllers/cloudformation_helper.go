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
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	cloudformationv1alpha1 "github.com/linki/cloudformation-operator/api/v1alpha1"
	"strings"
)

var (
	ErrStackNotFound = coreerrors.New("stack not found")
)

type CloudFormationHelper struct {
	CloudFormation *cloudformation.Client
}

// Identify if the follower considers the state identified as terminal.
func (cf *CloudFormationHelper) StackInTerminalState(status cfTypes.StackStatus) bool {
	statusString := string(status)
	if strings.HasSuffix(statusString, "_COMPLETE") {
		return true
	}
	if strings.HasSuffix(statusString, "_FAILED") {
		return true
	}
	return false
}

func (cf *CloudFormationHelper) GetStack(ctx context.Context, instance *cloudformationv1alpha1.Stack) (*cfTypes.Stack, error) {
	// Must use the stack ID to get details/finalization for deleted stacks
	name := instance.Status.StackID
	if name == "" {
		name = instance.Name
	}
	resp, err := cf.CloudFormation.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		NextToken: nil,
		StackName: aws.String(name),
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

func (cf *CloudFormationHelper) GetStackResources(ctx context.Context, stackId string) ([]cloudformationv1alpha1.StackResource, error) {

	var next *string
	next = nil
	toReturn := make([]cloudformationv1alpha1.StackResource, 0)

	for {
		resp, err := cf.CloudFormation.ListStackResources(ctx, &cloudformation.ListStackResourcesInput{
			NextToken: next,
			StackName: aws.String(stackId),
		})
		if err != nil {
			return nil, err
		}

		for _, e := range resp.StackResourceSummaries {
			reason := ""
			if e.ResourceStatusReason != nil {
				reason = *e.ResourceStatusReason
			}
			physicalID := ""
			if e.PhysicalResourceId != nil {
				physicalID = *e.PhysicalResourceId
			}

			resourceSummary := cloudformationv1alpha1.StackResource{
				LogicalId:    *e.LogicalResourceId,
				PhysicalId:   physicalID,
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
