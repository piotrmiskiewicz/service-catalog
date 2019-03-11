package controller_test

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

	assert.NoError(t, ct.WaitForReadyBroker())
	ct.CreateSecretWithBasicAuth("user1", "p2sswd")
	ct.CreateClusterServiceBrokerWithBasicAuth()

	// THEN
	ct.AssertOSBBasicAuth(t, "user1", "p2sswd")
	ct.AssertClusterServiceClassAndPlan(t)

	// uncomment when the https://github.com/kubernetes-incubator/service-catalog/issues/2563 is fixed
	//ct.UpdateSecretWithBasicAuth("user1", "newp2sswd")

	// WHEN
	ct.CreateServiceInstance()

	// THEN
	assert.NoError(t, ct.WaitForReadyInstance())

	// uncomment when the https://github.com/kubernetes-incubator/service-catalog/issues/2563 is fixed
	//ct.AssertOSBBasicAuth(t, "user1", "newp2sswd")

	// WHEN
	ct.CreateBinding()
	//THEN
	assert.NoError(t, ct.WaitForReadyBinding())

	// WHEN
	ct.DeleteBinding()
	ct.DeleteServiceInstance()

	// THEN
	assert.NoError(t, ct.WaitForServiceInstanceRemoved())
}

func TestBasicFlow(t *testing.T) {
	for tn, setupFunc := range map[string]func(ts *ControllerTest){
		"sync": func(ts *ControllerTest) {
		},
		"async instances with multiple polls": func(ct *ControllerTest) {
			ct.AsyncForInstances()
			ct.SetOSBPollLastOperationReactionInProgress(2)
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

			// WHEN
			ct.CreateServiceInstance()
			assert.NoError(t, ct.WaitForReadyInstance())

			// WHEN
			ct.CreateBinding()
			assert.NoError(t, ct.WaitForReadyBinding())

			// WHEN
			ct.DeleteBinding()
			ct.DeleteServiceInstance()
			// THEN
			assert.NoError(t, ct.WaitForServiceInstanceRemoved())
		})
	}
}

func TestServiceBindingOrphanMitigation(t *testing.T) {
	// GIVEN
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.SetOSBBindReactionWithHTTPError(http.StatusInternalServerError)
	ct.CreateSimpleClusterServiceBroker()
	assert.NoError(t, ct.WaitForReadyBroker())
	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())

	// WHEN
	ct.CreateBinding()

	// THEN
	assert.NoError(t, ct.WaitForBindingOrphanMitigationSuccessful())
}

func TestServiceBindingFailure(t *testing.T) {
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.SetOSBBindReactionWithHTTPError(http.StatusConflict)
	ct.CreateSimpleClusterServiceBroker()
	assert.NoError(t, ct.WaitForReadyBroker())
	ct.AssertClusterServiceClassAndPlan(t)
	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())

	// WHEN
	ct.CreateBinding()

	// THEN
	assert.NoError(t, ct.WaitForBindingFailed())
}

func TestServiceBindingRetryForNonExistingClass(t *testing.T) {
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.CreateSimpleClusterServiceBroker()
	assert.NoError(t, ct.WaitForReadyBroker())
	ct.AssertClusterServiceClassAndPlan(t)

	// WHEN
	ct.CreateBinding()
	assert.NoError(t, ct.WaitForNotReadyBinding())
	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())

	// THEN
	assert.NoError(t, ct.WaitForReadyBinding())
}


func TestProvisionInstanceWithRetries(t *testing.T) {
	// GIVEN
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.SetOSBProvisionReactionHTTPError(1)
	ct.CreateSimpleClusterServiceBroker()
	assert.NoError(t, ct.WaitForReadyBroker())

	// WHEN
	ct.CreateServiceInstance()

	// THEN
	assert.NoError(t, ct.WaitForReadyInstance())
}