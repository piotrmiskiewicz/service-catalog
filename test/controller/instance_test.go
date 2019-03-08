package it_test

import (
	"testing"
	"github.com/stretchr/testify/assert"

)

func TestProvisionInstanceWithRetries(t *testing.T) {
	// GIVEN
	ct := NewControllerTest(t)
	defer ct.TearDown()
	ct.SetProvisionReactionHTTPErrorInFirstCalls(1)

	// WHEN
	ct.CreateSimpleClusterServiceBroker()

	assert.NoError(t, ct.WaitForReadyBroker())

	// THEN
	ct.CreateServiceInstance()
	assert.NoError(t, ct.WaitForReadyInstance())


}
