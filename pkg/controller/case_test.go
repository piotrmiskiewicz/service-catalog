/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller_test

import (
	"fmt"
	"testing"
	"time"

	"encoding/json"
	"errors"
	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	fakesc "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/fake"
	scinterface "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/typed/servicecatalog/v1beta1"
	scinformers "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/externalversions"
	"github.com/kubernetes-incubator/service-catalog/pkg/controller"
	"github.com/pmorie/go-open-service-broker-client/v2"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	fakeosb "github.com/pmorie/go-open-service-broker-client/v2/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sinformers "k8s.io/client-go/informers"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
)

const (
	testNamespace                         = "test-ns"
	testClusterServiceBrokerName          = "test-clusterservicebroker"
	testClusterServiceClassName           = "test-clusterserviceclass"
	testClusterServicePlanName            = "test-clusterserviceplan"
	testServiceInstanceName               = "service-instance"
	testClassExternalID                   = "clusterserviceclass-12345"
	testPlanExternalID                    = "34567"
	testNonbindablePlanExternalID         = "nb34567"
	testNonbindableClusterServicePlanName = "test-nonbindable-plan"
	testExternalID                        = "9737b6ed-ca95-4439-8219-c53fcad118ab"
	testBindingName                       = "test-binding"
	testServiceBindingGUID                = "bguid"
	authSecretName                        = "basic-secret-name"
	testUsername                          = "some-user"

	pollingInterval = 50 * time.Millisecond
	pollingTimeout  = 8 * time.Second
)

// controllerTest provides helper methods to create and verify ServiceCatalog resources.
// Every test case needs a new instance of the controllerTest.
type controllerTest struct {
	// resource clientsets and interfaces
	scInterface      scinterface.ServicecatalogV1beta1Interface
	k8sClient        *fakek8s.Clientset
	fakeOSBClient    *fakeosb.FakeClient
	catalogReactions []fakeosb.CatalogReaction
	osbClientCfg     *v2.ClientConfiguration
	stopCh           chan struct{}
}

// newControllerTest creates a controllerTest instance with a ready to test running Controller
func newControllerTest(t *testing.T) *controllerTest {
	k8sClient := fakek8s.NewSimpleClientset()
	k8sClient.CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
		},
	})

	fakeOSBClient := fakeosb.NewFakeClient(fixtureHappyPathBrokerClientConfig())

	coreInformerFactory := k8sinformers.NewSharedInformerFactory(k8sClient, time.Minute)
	coreInformers := coreInformerFactory.Core()

	scClient := fakesc.NewSimpleClientset()
	informerFactory := scinformers.NewSharedInformerFactory(scClient, 0)
	serviceCatalogSharedInformers := informerFactory.Servicecatalog().V1beta1()

	clusterServiceClassInformer := serviceCatalogSharedInformers.ClusterServiceClasses()
	plansInformer := serviceCatalogSharedInformers.ClusterServicePlans()

	testCase := &controllerTest{
		scInterface:      scClient.ServicecatalogV1beta1(),
		k8sClient:        k8sClient,
		fakeOSBClient:    fakeOSBClient,
		catalogReactions: []fakeosb.CatalogReaction{},
	}

	// wrap the ClientFunc with a helper which saves last used OSG Client Config (it can be asserted in the test)
	brokerClFunc := testCase.spyOSBClientFunc(fakeosb.ReturnFakeClientFunc(fakeOSBClient))

	fakeRecorder := record.NewFakeRecorder(1)
	// start goroutine which flushes events (prevent hanging recording function)
	go func() {
		for range fakeRecorder.Events {
			// uncomment to see events
			//t.Log(err)
		}
	}()

	testController, err := controller.NewController(
		k8sClient,
		coreInformers.V1().Secrets(),
		scClient.ServicecatalogV1beta1(),
		serviceCatalogSharedInformers.ClusterServiceBrokers(),
		serviceCatalogSharedInformers.ServiceBrokers(),
		clusterServiceClassInformer,
		serviceCatalogSharedInformers.ServiceClasses(),
		serviceCatalogSharedInformers.ServiceInstances(),
		serviceCatalogSharedInformers.ServiceBindings(),
		plansInformer,
		serviceCatalogSharedInformers.ServicePlans(),
		brokerClFunc,
		time.Second,
		osb.LatestAPIVersion().HeaderValue(),
		fakeRecorder,
		7*24*time.Hour,
		7*24*time.Hour,
		"DefaultClusterIDConfigMapName",
		"DefaultClusterIDConfigMapNamespace",
	)
	if err != nil {
		t.Fatal(err)
	}

	// start and sync informers
	testCase.stopCh = make(chan struct{})
	informerFactory.Start(testCase.stopCh)
	coreInformerFactory.Start(testCase.stopCh)
	informerFactory.WaitForCacheSync(testCase.stopCh)
	coreInformerFactory.WaitForCacheSync(testCase.stopCh)

	// start the controller
	go testController.Run(1, testCase.stopCh)

	return testCase
}

func (ct *controllerTest) TearDown() {
	close(ct.stopCh)
}

// AsyncForInstanceProvisioning configures all fake OSB client provision
// responses with async flag
func (ct *controllerTest) AsyncForInstanceProvisioning() {
	ct.fakeOSBClient.ProvisionReaction.(*fakeosb.ProvisionReaction).Response.Async = true
}

// AsyncForInstanceUpdate configures all fake OSB client update
// responses with async flag
func (ct *controllerTest) AsyncForInstanceUpdate() {
	ct.fakeOSBClient.UpdateInstanceReaction.(*fakeosb.UpdateInstanceReaction).Response.Async = true
}

// AsyncForInstanceDeprovisioning configures all fake OSB client deprovision
// responses with async flag
func (ct *controllerTest) AsyncForInstanceDeprovisioning() {
	ct.fakeOSBClient.DeprovisionReaction.(*fakeosb.DeprovisionReaction).Response.Async = true
}

// AsyncForUnbind configures fake OSB client unbind operation responses with async flag
func (ct *controllerTest) AsyncForUnbind() {
	ct.fakeOSBClient.UnbindReaction.(*fakeosb.UnbindReaction).Response.Async = true
}

// AsyncForBind configures fake OSB client bind operation responses with async flag
func (ct *controllerTest) AsyncForBind() {
	ct.fakeOSBClient.BindReaction.(*fakeosb.BindReaction).Response.Async = true
}

// SyncForBindings configures all fake OSB client binding operations (bind and unbind)
// responses with async flag to false
func (ct *controllerTest) SyncForBindings() {
	ct.fakeOSBClient.BindReaction.(*fakeosb.BindReaction).Response.Async = false
	ct.fakeOSBClient.UnbindReaction.(*fakeosb.UnbindReaction).Response.Async = false
}

// AssertOSBBasicAuth verifies the last call to broker whether the correct basic auth credentials was used
func (ct *controllerTest) AssertOSBBasicAuth(t *testing.T, username, password string) {
	require.NotNil(t, ct.osbClientCfg, "OSB Client was not created, wait for broker is ready")
	assert.Equal(t, ct.osbClientCfg.AuthConfig.BasicAuthConfig, &v2.BasicAuthConfig{
		Username: username,
		Password: password,
	})
}

func (ct *controllerTest) NumberOfOSBUnbindingCalls() int {
	return ct.numberOfOSBActionByType(fakeosb.Unbind)
}

func (ct *controllerTest) NumberOfOSBBindingCalls() int {
	return ct.numberOfOSBActionByType(fakeosb.Bind)
}

func (ct *controllerTest) NumberOfOSBProvisionCalls() int {
	return ct.numberOfOSBActionByType(fakeosb.ProvisionInstance)
}

func (ct *controllerTest) NumberOfOSBDeprovisionCalls() int {
	return ct.numberOfOSBActionByType(fakeosb.DeprovisionInstance)
}

func (ct *controllerTest) numberOfOSBActionByType(actionType fakeosb.ActionType) int {
	actions := ct.fakeOSBClient.Actions()
	counter := 0
	for _, action := range actions {
		if action.Type == actionType {
			counter = counter + 1
		}
	}
	return counter
}

// SetFirstOSBPollLastOperationReactionsInProgress makes the broker
// responses inProgress in first numberOfInProgressResponses calls
func (ct *controllerTest) SetFirstOSBPollLastOperationReactionsInProgress(numberOfInProgressResponses int) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	numberOfPolls := 0
	ct.fakeOSBClient.PollLastOperationReaction = fakeosb.DynamicPollLastOperationReaction(
		func(_ *osb.LastOperationRequest) (*osb.LastOperationResponse, error) {
			numberOfPolls++
			state := osb.StateInProgress
			if numberOfPolls > numberOfInProgressResponses {
				state = osb.StateSucceeded
			}
			return &osb.LastOperationResponse{State: state}, nil
		})
}

// SetOSBPollLastOperationReactionsState makes the broker
// responses with given state
func (ct *controllerTest) SetOSBPollLastOperationReactionsState(state osb.LastOperationState) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	ct.fakeOSBClient.PollLastOperationReaction = &fakeosb.PollLastOperationReaction{
		Response: &osb.LastOperationResponse{State: state},
	}
}

// SetOSBPollBindingLastOperationReactionsState makes the broker
// responses with given state
func (ct *controllerTest) SetOSBPollBindingLastOperationReactionsState(state osb.LastOperationState) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	ct.fakeOSBClient.PollBindingLastOperationReaction = &fakeosb.PollBindingLastOperationReaction{
		Response: &osb.LastOperationResponse{State: state},
	}
}

// SetFirstOSBPollLastOperationReactionsInProgress makes the broker
// responses inProgress in first numberOfInProgressResponses calls
func (ct *controllerTest) SetFirstOSBPollLastOperationReactionsFailed(numberOfFailedResponses int) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	numberOfPolls := 0
	ct.fakeOSBClient.PollLastOperationReaction = fakeosb.DynamicPollLastOperationReaction(
		func(_ *osb.LastOperationRequest) (*osb.LastOperationResponse, error) {
			numberOfPolls++
			state := osb.StateFailed
			if numberOfPolls > numberOfFailedResponses {
				state = osb.StateSucceeded
			}
			return &osb.LastOperationResponse{State: state}, nil
		})
}

// SetFirstOSBProvisionReactionsHTTPError makes the broker
// responses with error in first numberOfInProgressResponses calls
func (ct *controllerTest) SetFirstOSBProvisionReactionsHTTPError(numberOfErrorResponses int, code int) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	numberOfPolls := 0
	ct.fakeOSBClient.ProvisionReaction = fakeosb.DynamicProvisionReaction(
		func(_ *osb.ProvisionRequest) (*osb.ProvisionResponse, error) {
			numberOfPolls++
			if numberOfPolls > numberOfErrorResponses {
				return &osb.ProvisionResponse{}, nil
			}
			return nil, osb.HTTPStatusCodeError{
				StatusCode: code,
			}
		})
}

// SetFirstOSBUnbindReactionsHTTPError makes the broker
// responses with error in first numberOfErrorResponses calls
func (ct *controllerTest) SetFirstOSBUnbindReactionsHTTPError(numberOfErrorResponses int, code int) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	numberOfPolls := 0
	ct.fakeOSBClient.UnbindReaction = fakeosb.DynamicUnbindReaction(
		func(_ *osb.UnbindRequest) (*osb.UnbindResponse, error) {
			numberOfPolls++
			if numberOfPolls > numberOfErrorResponses {
				return &osb.UnbindResponse{}, nil
			}
			return nil, osb.HTTPStatusCodeError{
				StatusCode: code,
			}
		})
}

// SetOSBBindReactionWithHTTPError configures the broker Bind call response as HTTPStatusCodeError
func (ct *controllerTest) SetOSBBindReactionWithHTTPError(code int) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()
	ct.fakeOSBClient.BindReaction = &fakeosb.BindReaction{
		Error: osb.HTTPStatusCodeError{
			StatusCode: code,
		},
	}
}

func (ct *controllerTest) spyOSBClientFunc(target v2.CreateFunc) v2.CreateFunc {
	return func(osbCfg *v2.ClientConfiguration) (v2.Client, error) {
		ct.osbClientCfg = osbCfg
		return target(osbCfg)
	}
}

func (ct *controllerTest) fixClusterServiceBroker() *v1beta1.ClusterServiceBroker {
	return &v1beta1.ClusterServiceBroker{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClusterServiceBrokerName,
		},
		Spec: v1beta1.ClusterServiceBrokerSpec{
			CommonServiceBrokerSpec: v1beta1.CommonServiceBrokerSpec{
				URL:            "https://broker.example.com",
				RelistBehavior: v1beta1.ServiceBrokerRelistBehaviorDuration,
				RelistDuration: &metav1.Duration{Duration: 15 * time.Minute},
			},
		},
	}
}

// CreateSimpleClusterServiceBroker creates a ClusterServiceBroker used in testing scenarios.
func (ct *controllerTest) CreateSimpleClusterServiceBroker() error {
	_, err := ct.scInterface.ClusterServiceBrokers().Create(ct.fixClusterServiceBroker())
	return err
}

// CreateClusterServiceBrokerWithBasicAuth creates a ClusterServiceBroker with basic auth.
func (ct *controllerTest) CreateClusterServiceBrokerWithBasicAuth() error {
	csb := ct.fixClusterServiceBroker()
	csb.Spec.AuthInfo = &v1beta1.ClusterServiceBrokerAuthInfo{
		Basic: &v1beta1.ClusterBasicAuthConfig{
			SecretRef: &v1beta1.ObjectReference{
				Name:      authSecretName,
				Namespace: testNamespace,
			},
		},
	}
	_, err := ct.scInterface.ClusterServiceBrokers().Create(csb)
	return err
}

// AddServiceClassRestrictionsToBroker updates a broker with a restrictions, which must filter out all existing classes.
func (ct *controllerTest) AddServiceClassRestrictionsToBroker() error {
	classes, err := ct.scInterface.ClusterServiceClasses().List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	var restrictions []string
	for _, cl := range classes.Items {
		restrictions = append(restrictions, fmt.Sprintf("name!=%s", cl.Name))
	}

	csb, err := ct.scInterface.ClusterServiceBrokers().Get(testClusterServiceBrokerName, metav1.GetOptions{})
	csb.Spec.CatalogRestrictions = &v1beta1.CatalogRestrictions{
		ServiceClass: restrictions,
	}
	csb.Generation = csb.Generation + 1
	_, err = ct.scInterface.ClusterServiceBrokers().Update(csb)
	return err
}

// CreateServiceInstance creates a ServiceInstance which is used in testing scenarios.
func (ct *controllerTest) CreateServiceInstance() error {
	_, err := ct.scInterface.ServiceInstances(testNamespace).Create(&v1beta1.ServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: testServiceInstanceName,
			// added by a Webhook, which is not tested here
			Finalizers: []string{v1beta1.FinalizerServiceCatalog},
		},
		Spec: v1beta1.ServiceInstanceSpec{
			PlanReference: v1beta1.PlanReference{
				ClusterServiceClassExternalName: testClassExternalID,
				ClusterServicePlanExternalName:  testPlanExternalID,
			},
			ExternalID: testExternalID,
			// Plan and Class refs are added by a Webhook, which is not tested here
			ClusterServicePlanRef: &v1beta1.ClusterObjectReference{
				Name: testPlanExternalID,
			},
			ClusterServiceClassRef: &v1beta1.ClusterObjectReference{
				Name: testClassExternalID,
			},
			UserInfo: fixtureUserInfo(),
		},
	})
	return err
}

func (ct *controllerTest) UpdateServiceInstanceParameters() error {
	si, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	parameters := map[string]interface{}{
		"param-key": "new-param-value",
	}
	marshalledParams, err := json.Marshal(parameters)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters %v : %v", parameters, err)
	}
	si.Spec.Parameters = &runtime.RawExtension{Raw: marshalledParams}
	si.Generation = si.Generation + 1

	_, err = ct.scInterface.ServiceInstances(testNamespace).Update(si)
	return err
}

// Deprovision sets deletion timestamp which is done by K8s in a cluster while ServiceInstance deletion.
func (ct *controllerTest) Deprovision() error {
	si, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return err
	}
	si.DeletionTimestamp = ct.v1Now()
	_, err = ct.scInterface.ServiceInstances(testNamespace).Update(si)
	return err
}

func (ct *controllerTest) CreateBinding() error {
	_, err := ct.scInterface.ServiceBindings(testNamespace).Create(&v1beta1.ServiceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  testNamespace,
			Name:       testBindingName,
			Generation: 1,
			Finalizers: []string{v1beta1.FinalizerServiceCatalog}, // set by the Webhook
		},
		Spec: v1beta1.ServiceBindingSpec{
			InstanceRef: v1beta1.LocalObjectReference{
				Name: testServiceInstanceName,
			},
			ExternalID: testServiceBindingGUID,
			SecretName: testBindingName, // set by the webhook
			UserInfo: fixtureUserInfo(),
		},
	})
	return err
}

// Unbind sets deletion timestamp which is done by K8s in a cluster. It triggers unbinding process.
func (ct *controllerTest) Unbind() error {
	sb, err := ct.scInterface.ServiceBindings(testNamespace).Get(testBindingName, v1.GetOptions{})
	if err != nil {
		return err
	}
	sb.DeletionTimestamp = ct.v1Now()
	_, err = ct.scInterface.ServiceBindings(testNamespace).Update(sb)
	return err
}

// DeleteBinding removes the ServiceBinding resource.
func (ct *controllerTest) DeleteBinding() error {
	return ct.scInterface.ServiceBindings(testNamespace).Delete(testBindingName, &v1.DeleteOptions{})
}

// CreateSecretWithBasicAuth creates a secret with credentials
// referenced by a ClusterServiceBroker created by CreateClusterServiceBrokerWithBasicAuth method.
func (ct *controllerTest) CreateSecretWithBasicAuth(username, password string) error {
	_, err := ct.k8sClient.CoreV1().Secrets(testNamespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      authSecretName,
		},
		Data: map[string][]byte{
			v1beta1.BasicAuthUsernameKey: []byte(username),
			v1beta1.BasicAuthPasswordKey: []byte(password),
		},
	})
	return err
}

// UpdateSecretWithBasicAuth updates a secret with basic auth
func (ct *controllerTest) UpdateSecretWithBasicAuth(username, password string) error {
	_, err := ct.k8sClient.CoreV1().Secrets(testNamespace).Update(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      authSecretName,
		},
		Data: map[string][]byte{
			v1beta1.BasicAuthUsernameKey: []byte(username),
			v1beta1.BasicAuthPasswordKey: []byte(password),
		},
	})
	return err
}

// MarkClusterServiceClassRemoved marks the cluster service class to be removed (sets the RemovedFromBrokerCatalog flag to true)
func (ct *controllerTest) MarkClusterServiceClassRemoved() error {
	csc, err := ct.scInterface.ClusterServiceClasses().Get(testClassExternalID, metav1.GetOptions{})
	if err != nil {
		return err
	}
	csc.Status.RemovedFromBrokerCatalog = true
	_, err = ct.scInterface.ClusterServiceClasses().UpdateStatus(csc)
	return err
}

// MarkClusterServicePlanRemoved marks the cluster service plan to be removed (sets the RemovedFromBrokerCatalog flag to true)
func (ct *controllerTest) MarkClusterServicePlanRemoved() error {
	csp, err := ct.scInterface.ClusterServicePlans().Get(testPlanExternalID, metav1.GetOptions{})
	if err != nil {
		return err
	}
	csp.Status.RemovedFromBrokerCatalog = true
	_, err = ct.scInterface.ClusterServicePlans().UpdateStatus(csp)
	return err
}

func (ct *controllerTest) AssertClusterServiceClassAndPlan(t *testing.T) {
	err := ct.WaitForClusterServiceClass()
	if err != nil {
		t.Fatal(err)
	}

	err = ct.WaitForClusterServicePlan()
	if err != nil {
		t.Fatal(err)
	}
}

func (ct *controllerTest) SetCatalogReactionError() {
	ct.fakeOSBClient.CatalogReaction = &fakeosb.CatalogReaction{
		Error: errors.New("ooops"),
	}
}

func (ct *controllerTest) WaitForReadyBinding() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionTrue,
	})
}

func (ct *controllerTest) WaitForNotReadyBinding() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionFalse,
	})
}

func (ct *controllerTest) WaitForBindingInProgress() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionFalse,
		Reason: "Binding",
	})
}

func (ct *controllerTest) WaitForBindingOrphanMitigationSuccessful() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionFalse,
		Reason: "OrphanMitigationSuccessful",
	})
}

func (ct *controllerTest) WaitForBindingFailed() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionFailed,
		Status: v1beta1.ConditionTrue,
	})
}

func (ct *controllerTest) WaitForUnbindStatus(status v1beta1.ServiceBindingUnbindStatus) error {
	var lastBinding *v1beta1.ServiceBinding
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		binding, err := ct.scInterface.ServiceBindings(testNamespace).Get(testBindingName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Binding: %v", err)
		}

		if binding.Status.UnbindStatus == status {
			return true, nil
		}

		lastBinding = binding
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("binding with proper unbinding status not found, the existing binding status: %+v", lastBinding.Status)
	}
	return err
}

func (ct *controllerTest) WaitForDeprovisionStatus(status v1beta1.ServiceInstanceDeprovisionStatus) error {
	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		si, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Binding: %v", err)
		}

		if si.Status.DeprovisionStatus == status {
			return true, nil
		}

		lastInstance = si
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("service instance with proper deprovision status not found, "+
			"the existing service instance status: %+v", lastInstance.Status)
	}
	return err
}

func (ct *controllerTest) waitForBindingStatusCondition(condition v1beta1.ServiceBindingCondition) error {
	var lastBinding *v1beta1.ServiceBinding
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		binding, err := ct.scInterface.ServiceBindings(testNamespace).Get(testBindingName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Binding: %v", err)
		}

		for _, cond := range binding.Status.Conditions {
			if condition.Type == cond.Type && condition.Status == cond.Status {
				if condition.Reason == "" || condition.Reason == cond.Reason {
					return true, nil
				}
			}
		}
		lastBinding = binding
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("binding with proper state not found, the existing binding status: %+v", lastBinding.Status)
	}
	return err
}

func (ct *controllerTest) WaitForServiceInstanceRemoved() error {
	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		lastInstance = instance
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("the service instance still exists: %+v", lastInstance)
	}
	return err
}

func (ct *controllerTest) WaitForReadyInstance() error {
	return ct.waitForInstanceCondition(v1beta1.ServiceInstanceCondition{
		Type:   v1beta1.ServiceInstanceConditionReady,
		Status: v1beta1.ConditionTrue,
	})
}

func (ct *controllerTest) WaitForInstanceUpdating() error {
	return ct.waitForInstanceCondition(v1beta1.ServiceInstanceCondition{
		Type:   v1beta1.ServiceInstanceConditionReady,
		Status: v1beta1.ConditionFalse,
		Reason: "UpdatingInstance",
	})
}

func (ct *controllerTest) waitForInstanceCondition(condition v1beta1.ServiceInstanceCondition) error {
	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Instance: %v", err)
		}
		lastInstance = instance

		for _, cond := range instance.Status.Conditions {
			if condition.Type == cond.Type && condition.Status == cond.Status {
				if condition.Reason == "" || condition.Reason == cond.Reason {
					return true, nil
				}
			}
		}
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("the instance is in expected state (expected condition %+v), current status: %+v", condition, lastInstance.Status)
	}
	return err
}

func (ct *controllerTest) WaitForAsyncProvisioningInProgress() error {
	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting ServiceInstance: %v", err)
		}
		lastInstance = instance

		if instance.Status.AsyncOpInProgress {
			return true, nil
		}

		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("the instance is not in progress, current status: %+v", lastInstance.Status)
	}
	return err
}

func (ct *controllerTest) WaitForReadyBroker() error {
	condition := v1beta1.ServiceBrokerCondition{
		Type:   v1beta1.ServiceBrokerConditionReady,
		Status: v1beta1.ConditionTrue,
	}

	var lastBroker *v1beta1.ClusterServiceBroker
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		broker, err := ct.scInterface.ClusterServiceBrokers().Get(testClusterServiceBrokerName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Broker: %v", err)
		}
		lastBroker = broker

		for _, cond := range broker.Status.Conditions {
			if condition.Type == cond.Type && condition.Status == cond.Status {
				if condition.Reason == "" || condition.Reason == cond.Reason {
					return true, nil
				}
			}
		}

		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("the broker is not ready, current status: %+v", lastBroker.Status)
	}
	return err
}

func (ct *controllerTest) WaitForClusterServiceClass() error {
	return wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		_, err := ct.scInterface.ClusterServiceClasses().Get(testClassExternalID, v1.GetOptions{})
		if err == nil {
			return true, nil
		}

		return false, err
	})
}

func (ct *controllerTest) WaitForClusterServiceClassToNotExists() error {
	return wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		_, err := ct.scInterface.ClusterServiceClasses().Get(testClassExternalID, v1.GetOptions{})
		if err != nil && apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, err
	})
}

func (ct *controllerTest) WaitForClusterServicePlanToNotExists() error {
	return wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		_, err := ct.scInterface.ClusterServicePlans().Get(testPlanExternalID, v1.GetOptions{})
		if err != nil && apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, err
	})
}

func (ct *controllerTest) WaitForClusterServicePlan() error {
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		_, err := ct.scInterface.ClusterServicePlans().Get(testPlanExternalID, v1.GetOptions{})
		if err == nil {
			return true, nil
		}

		return false, err
	})
	if err == wait.ErrWaitTimeout {
		plans, e := ct.scInterface.ClusterServicePlans().List(v1.ListOptions{})
		if e != nil {
			return err
		}
		return fmt.Errorf("plan %v not found, existing plans: %v", testPlanExternalID, plans)
	}
	return err
}

func (ct *controllerTest) AssertOSBRequestsUsername(t *testing.T) {
	for _, action := range ct.fakeOSBClient.Actions() {
		var oi *osb.OriginatingIdentity
		switch request := action.Request.(type) {
		case *osb.ProvisionRequest:
			oi = request.OriginatingIdentity
		case *osb.UpdateInstanceRequest:
			oi = request.OriginatingIdentity
		case *osb.DeprovisionRequest:
			oi = request.OriginatingIdentity
		case *osb.BindRequest:
			oi = request.OriginatingIdentity
		case *osb.UnbindRequest:
			oi = request.OriginatingIdentity
		case *osb.LastOperationRequest:
			oi = request.OriginatingIdentity
		default:
			continue
		}

		require.NotNil(t, oi, "originating identity of the request %v must not be nil", action.Type)

		oiValues := make(map[string]interface{})
		require.NoError(t, json.Unmarshal([]byte(oi.Value), &oiValues))

		if e, a := testUsername, oiValues["username"]; e != a {
			t.Fatalf("unexpected username in originating identity: expected %q, got %q", e, a)
		}
	}
}

func (ct *controllerTest) v1Now() *metav1.Time {
	n := v1.NewTime(time.Now())
	return &n
}

func fixtureHappyPathBrokerClientConfig() fakeosb.FakeClientConfiguration {
	return fakeosb.FakeClientConfiguration{
		CatalogReaction: &fakeosb.CatalogReaction{
			Response: fixtureCatalogResponse(),
		},
		ProvisionReaction: &fakeosb.ProvisionReaction{
			Response: &osb.ProvisionResponse{},
		},
		UpdateInstanceReaction: &fakeosb.UpdateInstanceReaction{
			Response: &osb.UpdateInstanceResponse{},
		},
		DeprovisionReaction: &fakeosb.DeprovisionReaction{
			Response: &osb.DeprovisionResponse{},
		},
		BindReaction: &fakeosb.BindReaction{
			Response: &osb.BindResponse{
				Credentials: fixtureBindCredentials(),
			},
		},
		UnbindReaction: &fakeosb.UnbindReaction{
			Response: &osb.UnbindResponse{},
		},
		PollLastOperationReaction: &fakeosb.PollLastOperationReaction{
			Response: &osb.LastOperationResponse{
				State: osb.StateSucceeded,
			},
		},
		PollBindingLastOperationReaction: &fakeosb.PollBindingLastOperationReaction{
			Response: &osb.LastOperationResponse{
				State: osb.StateSucceeded,
			},
		},
		GetBindingReaction: &fakeosb.GetBindingReaction{
			Response: &osb.GetBindingResponse{
				Credentials: fixtureBindCredentials(),
			},
		},
	}
}

// fixtureCatalogResponse returns a sample response to a get catalog request.
func fixtureCatalogResponse() *osb.CatalogResponse {
	return &osb.CatalogResponse{
		Services: []osb.Service{
			{
				Name:        testClusterServiceClassName,
				ID:          testClassExternalID,
				Description: "a test service",
				Bindable:    true,
				Plans: []osb.Plan{
					{
						Name:        testClusterServicePlanName,
						Free:        truePtr(),
						ID:          testPlanExternalID,
						Description: "a test plan",
					},
					{
						Name:        testNonbindableClusterServicePlanName,
						Free:        truePtr(),
						ID:          testNonbindablePlanExternalID,
						Description: "an non-bindable test plan",
						Bindable:    falsePtr(),
					},
				},
			},
		},
	}
}

// fixtureBindCredentials returns binding credentials to include in the response
// to a bind request.
func fixtureBindCredentials() map[string]interface{} {
	return map[string]interface{}{
		"foo": "bar",
		"baz": "zap",
	}
}

func fixtureUserInfo() *v1beta1.UserInfo{
	return &v1beta1.UserInfo{
		Username: testUsername,
	}
}

func truePtr() *bool {
	b := true
	return &b
}

func falsePtr() *bool {
	b := false
	return &b
}
