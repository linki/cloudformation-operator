package fake

import (
	v1alpha1 "github.com/linki/cloudformation-operator/pkg/apis/cloudformation/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeStacks implements StackInterface
type FakeStacks struct {
	Fake *FakeCloudformationV1alpha1
	ns   string
}

var stacksResource = schema.GroupVersionResource{Group: "cloudformation.linki.space", Version: "v1alpha1", Resource: "stacks"}

var stacksKind = schema.GroupVersionKind{Group: "cloudformation.linki.space", Version: "v1alpha1", Kind: "Stack"}

// Get takes name of the stack, and returns the corresponding stack object, and an error if there is any.
func (c *FakeStacks) Get(name string, options v1.GetOptions) (result *v1alpha1.Stack, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(stacksResource, c.ns, name), &v1alpha1.Stack{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Stack), err
}

// List takes label and field selectors, and returns the list of Stacks that match those selectors.
func (c *FakeStacks) List(opts v1.ListOptions) (result *v1alpha1.StackList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(stacksResource, stacksKind, c.ns, opts), &v1alpha1.StackList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.StackList{}
	for _, item := range obj.(*v1alpha1.StackList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested stacks.
func (c *FakeStacks) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(stacksResource, c.ns, opts))

}

// Create takes the representation of a stack and creates it.  Returns the server's representation of the stack, and an error, if there is any.
func (c *FakeStacks) Create(stack *v1alpha1.Stack) (result *v1alpha1.Stack, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(stacksResource, c.ns, stack), &v1alpha1.Stack{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Stack), err
}

// Update takes the representation of a stack and updates it. Returns the server's representation of the stack, and an error, if there is any.
func (c *FakeStacks) Update(stack *v1alpha1.Stack) (result *v1alpha1.Stack, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(stacksResource, c.ns, stack), &v1alpha1.Stack{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Stack), err
}

// Delete takes name of the stack and deletes it. Returns an error if one occurs.
func (c *FakeStacks) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteAction(stacksResource, c.ns, name), &v1alpha1.Stack{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeStacks) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(stacksResource, c.ns, listOptions)

	_, err := c.Fake.Invokes(action, &v1alpha1.StackList{})
	return err
}

// Patch applies the patch and returns the patched stack.
func (c *FakeStacks) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.Stack, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(stacksResource, c.ns, name, data, subresources...), &v1alpha1.Stack{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Stack), err
}
