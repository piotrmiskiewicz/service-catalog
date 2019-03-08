package it_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	scfeatures "github.com/kubernetes-incubator/service-catalog/pkg/features"
)

func TestBasicFlowWithBasicAuth(t *testing.T) {
	// GIVEN
	ct := NewControllerTest(t)
	defer ct.TearDown()

	// WHEN
	ct.CreateSecretWithBasicAuth("user1", "p2sswd")
	ct.CreateClusterServiceBrokerWithBasicAuth()

	// THEN
	assert.NoError(t, ct.WaitForReadyBroker())
	ct.CreateSecretWithBasicAuth("user1", "p2sswd")
	ct.CreateClusterServiceBrokerWithBasicAuth()
	ct.AssertOSBBasicAuth(t, "user1", "p2sswd")


	ct.AssertClusterServiceClassAndPlan(t)

	// uncomment when the https://github.com/kubernetes-incubator/service-catalog/issues/2563 is fixed
	ct.UpdateSecretWithBasicAuth("user1", "newp2sswd")

	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())

	// uncomment when the https://github.com/kubernetes-incubator/service-catalog/issues/2563 is fixed
	ct.AssertOSBBasicAuth(t, "user1", "newp2sswd")

	ct.CreateBinding()
	assert.NoError(t, ct.WaitForReadyBinding())

	ct.DeleteBinding()

	ct.DeleteServiceInstance()

	assert.NoError(t, ct.WaitForServiceInstanceRemoved())
}

func TestBasicFlow(t *testing.T) {
	for tn, setupFunc := range map[string]func(ts *ControllerTest){
		"sync": func(ts *ControllerTest) {
		},
		"async instances with multiple polls": func(ct *ControllerTest) {
			ct.AsyncForInstances()
			ct.SetPollLastOperationInProgressInFirstCalls(2)
		},
		"async bindings": func(ct *ControllerTest) {
			ct.AsyncForBindings()
		},
		"async instances and bindings": func(ct *ControllerTest) {
			ct.AsyncForInstances()
			ct.AsyncForBindings()
		},
	} {
		t.Run(tn, func(t *testing.T) {
			t.Parallel()
			// GIVEN
			utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%v=true", scfeatures.AsyncBindingOperations))
			defer utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%v=false", scfeatures.AsyncBindingOperations))
			ct := NewControllerTest(t)
			defer ct.TearDown()
			setupFunc(ct)

			// WHEN
			ct.CreateSimpleClusterServiceBroker()

			// THEN
			assert.NoError(t, ct.WaitForReadyBroker())

			ct.AssertClusterServiceClassAndPlan(t)

			ct.CreateServiceInstance()
			assert.NoError(t, ct.WaitForReadyInstance())

			ct.CreateBinding()

			assert.NoError(t, ct.WaitForReadyBinding())

			ct.DeleteBinding()

			ct.DeleteServiceInstance()

			assert.NoError(t, ct.WaitForServiceInstanceRemoved())
		})
	}
}

func TestServiceBindingOrphanMitigation(t *testing.T) {
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.SetBindReactionWithHTTPError(http.StatusInternalServerError)

	// WHEN
	ct.CreateSimpleClusterServiceBroker()

	assert.NoError(t, ct.WaitForReadyBroker())

	ct.AssertClusterServiceClassAndPlan(t)

	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())

	ct.CreateBinding()

	assert.NoError(t, ct.WaitForBindingOrphanMitigationSuccessful())
}

func TestServiceBindingFailure(t *testing.T) {
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.SetBindReactionWithHTTPError(http.StatusConflict)

	// WHEN
	ct.CreateSimpleClusterServiceBroker()

	assert.NoError(t, ct.WaitForReadyBroker())

	ct.AssertClusterServiceClassAndPlan(t)

	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())

	ct.CreateBinding()

	assert.NoError(t, ct.WaitForBindingFailed())
}
