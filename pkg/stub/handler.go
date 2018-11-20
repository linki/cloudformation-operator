package stub

import (
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-sdk/pkg/sdk/action"
	"github.com/operator-framework/operator-sdk/pkg/sdk/handler"
	"github.com/operator-framework/operator-sdk/pkg/sdk/types"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/aws/aws-sdk-go/service/cloudformation/cloudformationiface"

	"github.com/linki/cloudformation-operator/pkg/apis/cloudformation/v1alpha1"
)

const (
	ownerTagKey   = "kubernetes.io/controlled-by"
	ownerTagValue = "cloudformation.linki.space/operator"
)

var (
	ErrStackNotFound = errors.New("stack not found")
)

type Handler struct {
	client       cloudformationiface.CloudFormationAPI
	capabilities []string
	defautTags   map[string]string
	dryRun       bool
}

func NewHandler(client cloudformationiface.CloudFormationAPI, capabilities []string, defautTags map[string]string, dryRun bool) handler.Handler {
	return &Handler{client: client, capabilities: capabilities, defautTags: defautTags, dryRun: dryRun}
}

func (h *Handler) Handle(ctx types.Context, event types.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.Stack:
		stack := o

		// Check if we have ownership over the stack. If the stack exists it must have the correct tag
		// set. If the stack doesn't exist we take the ownership.
		ownedByUs, err := h.hasOwnership(stack)
		if err != nil {
			return err
		}

		if !ownedByUs {
			logrus.WithField("stack", stack.Name).Info("stack not owned by us, ignoring")
			return nil
		}

		if event.Deleted {
			return h.deleteStack(stack)
		}

		exists, err := h.stackExists(stack)
		if err != nil {
			return err
		}

		if exists {
			return h.updateStack(stack)
		}

		return h.createStack(stack)
	}

	return nil
}

func (h *Handler) createStack(stack *v1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("creating stack")

	if h.dryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack creation")
		return nil
	}

	input := &cloudformation.CreateStackInput{
		Capabilities: aws.StringSlice(h.capabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   stackParameters(stack),
		Tags:         stackTags(stack, h.defautTags),
	}

	if _, err := h.client.CreateStack(input); err != nil {
		return err
	}

	if err := h.waitWhile(stack, cloudformation.StackStatusCreateInProgress); err != nil {
		return err
	}

	return h.updateStackStatus(stack)
}

func (h *Handler) updateStack(stack *v1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("updating stack")

	if h.dryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack update")
		return nil
	}

	input := &cloudformation.UpdateStackInput{
		Capabilities: aws.StringSlice(h.capabilities),
		StackName:    aws.String(stack.Name),
		TemplateBody: aws.String(stack.Spec.Template),
		Parameters:   stackParameters(stack),
		Tags:         stackTags(stack, h.defautTags),
	}

	if _, err := h.client.UpdateStack(input); err != nil {
		if strings.Contains(err.Error(), "No updates are to be performed.") {
			logrus.WithField("stack", stack.Name).Debug("stack already updated")
			return nil
		}
		return err
	}

	if err := h.waitWhile(stack, cloudformation.StackStatusUpdateInProgress); err != nil {
		return err
	}

	return h.updateStackStatus(stack)
}

func (h *Handler) deleteStack(stack *v1alpha1.Stack) error {
	logrus.WithField("stack", stack.Name).Info("deleting stack")

	if h.dryRun {
		logrus.WithField("stack", stack.Name).Info("skipping stack deletion")
		return nil
	}

	input := &cloudformation.DeleteStackInput{
		StackName: aws.String(stack.Name),
	}

	if _, err := h.client.DeleteStack(input); err != nil {
		return err
	}

	return h.waitWhile(stack, cloudformation.StackStatusDeleteInProgress)
}

func (h *Handler) getStack(stack *v1alpha1.Stack) (*cloudformation.Stack, error) {
	resp, err := h.client.DescribeStacks(&cloudformation.DescribeStacksInput{
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

func (h *Handler) stackExists(stack *v1alpha1.Stack) (bool, error) {
	_, err := h.getStack(stack)
	if err != nil {
		if err == ErrStackNotFound {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (h *Handler) hasOwnership(stack *v1alpha1.Stack) (bool, error) {
	exists, err := h.stackExists(stack)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}

	cfs, err := h.getStack(stack)
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

func (h *Handler) updateStackStatus(stack *v1alpha1.Stack) error {
	cfs, err := h.getStack(stack)
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

		if err := action.Update(stack); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handler) waitWhile(stack *v1alpha1.Stack, status string) error {
	for {
		cfs, err := h.getStack(stack)
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
func stackParameters(stack *v1alpha1.Stack) []*cloudformation.Parameter {
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
func stackTags(stack *v1alpha1.Stack, defaultTags map[string]string) []*cloudformation.Tag {
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
