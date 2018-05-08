package fake

import (
	v1alpha1 "github.com/enekofb/cloudformation-operator/pkg/client/clientset/versioned/typed/cloudformation/v1alpha1"
	rest "k8s.io/client-go/rest"
	testing "k8s.io/client-go/testing"
)

type FakeCloudformationV1alpha1 struct {
	*testing.Fake
}

func (c *FakeCloudformationV1alpha1) Stacks(namespace string) v1alpha1.StackInterface {
	return &FakeStacks{c, namespace}
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *FakeCloudformationV1alpha1) RESTClient() rest.Interface {
	var ret *rest.RESTClient
	return ret
}
