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
	"net/http"
	"testing"
	"time"

	"encoding/json"
	"errors"
	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	fakesc "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/fake"
	scinterface "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/typed/servicecatalog/v1beta1"
	scinformers "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/externalversions"
	"github.com/kubernetes-incubator/service-catalog/pkg/controller"
	scfeatures "github.com/kubernetes-incubator/service-catalog/pkg/features"
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
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	k8sinformers "k8s.io/client-go/informers"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"reflect"
)

const (
	testNamespace                         = "test-ns"
	testClusterServiceBrokerName          = "test-clusterservicebroker"
	testClusterServiceClassName           = "test-clusterserviceclass"
	testClusterServicePlanName            = "test-clusterserviceplan"
	testOtherClusterServicePlanName       = "test-otherclusterserviceplan"
	testServiceInstanceName               = "service-instance"
	testClassExternalID                   = "clusterserviceclass-12345"
	testPlanExternalID                    = "34567"
	testOtherPlanExternalID               = "76543"
	testNonbindablePlanExternalID         = "nb34567"
	testNonbindableClusterServicePlanName = "test-nonbindable-plan"
	testExternalID                        = "9737b6ed-ca95-4439-8219-c53fcad118ab"
	testBindingName                       = "test-binding"
	testServiceBindingGUID                = "bguid"
	authSecretName                        = "basic-secret-name"
	testUsername                          = "some-user"
	secretNameWithParameters              = "secret-name"
	secretKeyWithParameters               = "secret-key"
	otherSecretNameWithParameters         = "other-secret-name"
	otherSecretKeyWithParameters          = "other-secret-key"
	testDashboardURL                      = "http://test-dashboard.example.com"

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
			//for err := range fakeRecorder.Events {
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
			UserInfo:   fixtureUserInfo(),
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

// TimeoutError simulates timeout error in provision ServiceInstance test
type TimeoutError string

// Timeout method require for TimeoutError type to meet the url/timeout interface
func (e TimeoutError) Timeout() bool {
	return true
}

// Error returns the TimeoutError as a string
func (e TimeoutError) Error() string {
	return string(e)
}

// SetupEmptyPlanListForOSBClient sets up fake OSB client response to return plans which not exist in any ServiceInstance
func (ct *controllerTest) SetupEmptyPlanListForOSBClient() {
	ct.fakeOSBClient.CatalogReaction.(*fakeosb.CatalogReaction).Response = &osb.CatalogResponse{
		Services: []osb.Service{
			{
				Name:        testClusterServiceClassName,
				ID:          testClassExternalID,
				Description: "a test service",
				Bindable:    true,
				Plans: []osb.Plan{
					{
						Name:        "randomPlan",
						Free:        truePtr(),
						ID:          "randomID",
						Description: "This is plan which should not exist in any of instance",
					},
				},
			},
		},
	}
}

// CreateServiceBrokerWithIncreaseRelistRequests creates ServiceBroker then increase by one `spec.relistRequests` field
// and updates the ServiceBroker
func (ct *controllerTest) CreateServiceBrokerWithIncreaseRelistRequests() error {
	broker := ct.fixClusterServiceBroker()
	_, err := ct.scInterface.ClusterServiceBrokers().Create(broker)
	if err != nil {
		return err
	}

	broker.Spec.RelistRequests++
	_, err = ct.scInterface.ClusterServiceBrokers().Update(broker)

	return err
}

// WaitForInstanceCondition waits until ServiceInstance `status.conditions` field value is equal to condition in parameters
// returns error if the time limit has been reached
func (ct *controllerTest) WaitForInstanceCondition(condition v1beta1.ServiceInstanceCondition) error {
	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Instance: %v", err)
		}

		for _, cond := range instance.Status.Conditions {
			if condition.Type == cond.Type && condition.Status == cond.Status && condition.Reason == cond.Reason {
				return true, nil
			}
		}
		lastInstance = instance
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf(
			"instance with proper conditions not found, the existing conditions: %+v", lastInstance.Status.Conditions)
	}
	return err
}

// WaitForServiceInstanceProcessedGeneration waits until ServiceInstance parameter `Status.ObservedGeneration` is
// equal or higher than ServiceInstance `generation` value, ServiceInstance is in Ready/True status and
// ServiceInstance is not in Orphan Mitigation progress
func (ct *controllerTest) WaitForServiceInstanceProcessedGeneration(generation int64) error {
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Instance: %v", err)
		}

		if instance.Status.ObservedGeneration >= generation &&
			isServiceInstanceConditionTrue(instance) &&
			!instance.Status.OrphanMitigationInProgress {
			return true, nil
		}

		return false, nil
	})

	if err == wait.ErrWaitTimeout {
		return fmt.Errorf(
			"instance with proper ProcessedGeneration status not found")
	}
	return err
}

func isServiceInstanceConditionTrue(instance *v1beta1.ServiceInstance) bool {
	for _, cond := range instance.Status.Conditions {
		if cond.Type == v1beta1.ServiceInstanceConditionReady || cond.Type == v1beta1.ServiceInstanceConditionFailed {
			return cond.Status == v1beta1.ConditionTrue
		}
	}

	return false
}

// EnsureServiceInstanceHasNoCondition makes sure ServiceInstance is in not specific condition
func (ct *controllerTest) EnsureServiceInstanceHasNoCondition(cond v1beta1.ServiceInstanceCondition) error {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting Instance: %v", err)
	}

	for _, condition := range instance.Status.Conditions {
		if t1, t2 := condition.Type, cond.Type; t1 == t2 {
			if s1, s2 := condition.Status, cond.Status; s1 == s2 {
				return fmt.Errorf(
					"unexpected condition status: expected %v, got %v or \n "+
						"unexpected condition type: expected %v, got %v", s2, s1, t2, t1)
			}
		}
	}

	return nil
}

// EnsureServiceInstanceHasCondition makes sure is in specific condition
func (ct *controllerTest) EnsureServiceInstanceHasCondition(cond v1beta1.ServiceInstanceCondition) error {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting Instance: %v", err)
	}

	foundCondition := false
	for _, condition := range instance.Status.Conditions {
		if condition.Type == cond.Type {
			foundCondition = true
			if condition.Status != cond.Status {
				return fmt.Errorf(
					"condition had unexpected status; expected %v, got %v", cond.Status, condition.Status)
			}
			if condition.Reason != cond.Reason {
				return fmt.Errorf(
					"unexpected reason; expected %v, got %v", cond.Reason, condition.Reason)
			}
		}
	}

	if !foundCondition {
		return fmt.Errorf("condition not found: %v", cond.Type)
	}

	return nil
}

// EnsureServiceInstanceOrphanMitigationStatus makes sure ServiceInstance is/or is not in Orphan Mitigation progress
func (ct *controllerTest) EnsureServiceInstanceOrphanMitigationStatus(state bool) error {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting Instance: %v", err)
	}

	if om := instance.Status.OrphanMitigationInProgress; om != state {
		return fmt.Errorf("unexpected OrphanMitigationInProgress status: expected %v, got %v", state, om)
	}

	return nil
}

// CreateClusterServiceClass creates ClusterServiceClass with default parameters
func (ct *controllerTest) CreateClusterServiceClass() error {
	serviceClass := &v1beta1.ClusterServiceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClassExternalID,
		},
		Spec: v1beta1.ClusterServiceClassSpec{
			ClusterServiceBrokerName: testClusterServiceBrokerName,
			CommonServiceClassSpec: v1beta1.CommonServiceClassSpec{
				ExternalID:   testClassExternalID,
				ExternalName: testClusterServiceClassName,
				Description:  "a test service",
				Bindable:     true,
			},
		},
	}
	if _, err := ct.scInterface.ClusterServiceClasses().Create(serviceClass); err != nil {
		return err
	}

	return nil
}

// CreateClusterServicePlan creates CreateClusterServicePlan with default parameters
func (ct *controllerTest) CreateClusterServicePlan() error {
	servicePlan := &v1beta1.ClusterServicePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name: testPlanExternalID,
		},
		Spec: v1beta1.ClusterServicePlanSpec{
			ClusterServiceBrokerName: testClusterServicePlanName,
		},
	}
	if _, err := ct.scInterface.ClusterServicePlans().Create(servicePlan); err != nil {
		return err
	}

	return nil
}

// UpdateServiceInstanceExternalPlanName updates ServiceInstance plan by plan ID
func (ct *controllerTest) UpdateServiceInstanceExternalPlanName(planID string) (int64, error) {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("error getting Instance: %v", err)
	}

	instance.Spec.ClusterServicePlanExternalName = planID
	instance.Spec.ClusterServicePlanRef = &v1beta1.ClusterObjectReference{
		Name: planID,
	}

	instance.Generation = instance.Generation + 1
	updatedInstance, err := ct.scInterface.ServiceInstances(testNamespace).Update(instance)

	if err != nil {
		return 0, fmt.Errorf("error updating Instance: %v", err)
	}

	return updatedInstance.Generation, nil
}

// UpdateServiceInstanceInternalPlanName updates ServiceInstance plan by plan name
// CAUTION: because Plan refs are added by a Webhook tests require adds planRef before update ServiceInstance
func (ct *controllerTest) UpdateServiceInstanceInternalPlanName(planName string) (int64, error) {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("error getting Instance: %v", err)
	}

	instance.Spec.ClusterServicePlanName = planName
	instance.Spec.ClusterServicePlanRef = &v1beta1.ClusterObjectReference{
		Name: testOtherPlanExternalID,
	}

	instance.Generation = instance.Generation + 1
	updatedInstance, err := ct.scInterface.ServiceInstances(testNamespace).Update(instance)

	if err != nil {
		return 0, fmt.Errorf("error updating Instance: %v", err)
	}

	return updatedInstance.Generation, nil
}

// CreateServiceInstanceWithCustomParameters creates ServiceInstance with parameters from map or
// by adding reference to Secret. If parameters are empty method creates ServiceInstance without parameters
func (ct *controllerTest) CreateServiceInstanceWithCustomParameters(withParam, paramFromSecret bool) error {
	var params map[string]interface{}
	var paramsFrom []v1beta1.ParametersFromSource

	if withParam {
		params = map[string]interface{}{
			"param-key": "param-value",
		}
	}

	if paramFromSecret {
		paramsFrom = []v1beta1.ParametersFromSource{
			{
				SecretKeyRef: &v1beta1.SecretKeyReference{
					Name: secretNameWithParameters,
					Key:  secretKeyWithParameters,
				},
			},
		}
	}

	var err error
	if withParam || paramFromSecret {
		_, err = ct.CreateServiceInstanceWithParameters(params, paramsFrom)
	} else {
		err = ct.CreateServiceInstance()
	}

	if err != nil {
		return err
	}

	return nil
}

// CreateServiceInstanceWithParameters creates ServiceInstance with parameters from map and by adding
// Secret reference
func (ct *controllerTest) CreateServiceInstanceWithParameters(
	params map[string]interface{},
	paramsFrom []v1beta1.ParametersFromSource) (*v1beta1.ServiceInstance, error) {
	rawParams, err := convertParametersIntoRawExtension(params)
	if err != nil {
		return nil, err
	}

	instance := &v1beta1.ServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testServiceInstanceName,
			Finalizers: []string{v1beta1.FinalizerServiceCatalog},
		},
		Spec: v1beta1.ServiceInstanceSpec{
			PlanReference: v1beta1.PlanReference{
				ClusterServiceClassExternalName: testClusterServiceClassName,
				ClusterServicePlanExternalName:  testClusterServicePlanName,
			},
			ClusterServicePlanRef: &v1beta1.ClusterObjectReference{
				Name: testPlanExternalID,
			},
			ClusterServiceClassRef: &v1beta1.ClusterObjectReference{
				Name: testClassExternalID,
			},
			ExternalID:     testExternalID,
			Parameters:     rawParams,
			ParametersFrom: paramsFrom,
		},
	}

	_, err = ct.scInterface.ServiceInstances(testNamespace).Create(instance)
	if err != nil {
		return nil, err
	}

	return instance, err
}

// UpdateCustomServiceInstanceParameters updates ServiceInstance with specific parameters. Method updates
// directly parameters, parameters by adding Secret reference, removes parameters or removes reference to Secret
func (ct *controllerTest) UpdateCustomServiceInstanceParameters(
	update, updateFromSecret, delete, deleteFromSecret bool) (int64, error) {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return 0, err
	}

	if update {
		instanceParam, err := convertParametersIntoRawExtension(map[string]interface{}{"param-key": "new-param-value"})
		if err != nil {
			return 0, err
		}
		instance.Spec.Parameters = instanceParam
	}

	if delete {
		instance.Spec.Parameters = nil
	}

	if updateFromSecret {
		instance.Spec.ParametersFrom = []v1beta1.ParametersFromSource{
			{
				SecretKeyRef: &v1beta1.SecretKeyReference{
					Name: otherSecretNameWithParameters,
					Key:  otherSecretKeyWithParameters,
				},
			},
		}
	}

	if deleteFromSecret {
		instance.Spec.ParametersFrom = nil
	}

	instance.Generation = instance.Generation + 1
	updatedInstance, err := ct.scInterface.ServiceInstances(testNamespace).Update(instance)
	if err != nil {
		return 0, err
	}

	return updatedInstance.Generation, nil
}

func convertParametersIntoRawExtension(parameters map[string]interface{}) (*runtime.RawExtension, error) {
	marshalledParams, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	return &runtime.RawExtension{Raw: marshalledParams}, nil
}

// CreateServiceInstanceWithInvalidParameters creates instance and updates parameters with incorrect parameters
func (ct *controllerTest) CreateServiceInstanceWithInvalidParameters() error {
	params := map[string]interface{}{
		"Name": "test-param",
		"Args": map[string]interface{}{
			"first":  "first-arg",
			"second": "second-arg",
		},
	}
	instance, err := ct.CreateServiceInstanceWithParameters(params, nil)
	if err != nil {
		return err
	}

	instance.Spec.Parameters.Raw[0] = 0x21
	instance.Generation = instance.Generation + 1

	_, err = ct.scInterface.ServiceInstances(testNamespace).Update(instance)
	if err != nil {
		return err
	}

	return nil
}

// EnsureInstanceObservedGeneration makes sure ServiceInstance status `ObservedGeneration` parameter is not
// equal to generation argument
func (ct *controllerTest) EnsureInstanceObservedGeneration(gen int64) error {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return err
	}
	if val := instance.Status.ObservedGeneration; val != gen {
		return fmt.Errorf("unexpected observed generation: expected %v, got %v", gen, val)
	}

	return nil
}

// EnsureObservedGenerationIsCorrect makes sure ServiceInstance status `ObservedGeneration` parameter is not
// equal to ServiceInstance `Generation` parameter
func (ct *controllerTest) EnsureObservedGenerationIsCorrect() error {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return err
	}

	if g, og := instance.Generation, instance.Status.ObservedGeneration; g != og {
		return fmt.Errorf("latest generation not observed: generation: %v, observed: %v", g, og)
	}

	return nil
}

// SetErrorReactionForProvisioningToOSBClient sets up DynamicProvisionReaction for fake osb client with specific
// error status code
func (ct *controllerTest) SetErrorReactionForProvisioningToOSBClient(statusCode int) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	ct.fakeOSBClient.ProvisionReaction = fakeosb.DynamicProvisionReaction(
		func(_ *osb.ProvisionRequest) (*osb.ProvisionResponse, error) {
			return nil, osb.HTTPStatusCodeError{
				StatusCode:   statusCode,
				ErrorMessage: strPtr("error message"),
				Description:  strPtr("response description"),
			}
		})
}

// SetCustomErrorReactionForProvisioningToOSBClient sets up DynamicProvisionReaction for fake osb client
// with specific response
func (ct *controllerTest) SetCustomErrorReactionForProvisioningToOSBClient(response error) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	ct.fakeOSBClient.ProvisionReaction = fakeosb.DynamicProvisionReaction(
		func(_ *osb.ProvisionRequest) (*osb.ProvisionResponse, error) {
			return nil, response
		})
}

// SetErrorReactionForDeprovisioningToOSBClient sets up DynamicDeprovisionReaction for fake osb client with specific
// error status code. Method allows blocking deprovision response
func (ct *controllerTest) SetErrorReactionForDeprovisioningToOSBClient(statusCode int, block <-chan bool) {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	blockDeprovision := true
	ct.fakeOSBClient.DeprovisionReaction = fakeosb.DynamicDeprovisionReaction(
		func(_ *osb.DeprovisionRequest) (*osb.DeprovisionResponse, error) {
			for blockDeprovision {
				blockDeprovision = <-block
			}
			return nil, osb.HTTPStatusCodeError{
				StatusCode:   statusCode,
				ErrorMessage: strPtr("temporary deprovision error"),
			}
		})
}

// SetSuccessfullyReactionForProvisioningToOSBClient sets up DynamicProvisionReaction for fake osb client
// with success response
func (ct *controllerTest) SetSuccessfullyReactionForProvisioningToOSBClient() {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	ct.fakeOSBClient.ProvisionReaction = fakeosb.DynamicProvisionReaction(
		func(_ *osb.ProvisionRequest) (*osb.ProvisionResponse, error) {
			return &osb.ProvisionResponse{}, nil
		})
}

// SetSuccessfullyReactionForDeprovisioningToOSBClient sets up DynamicDeprovisionReaction for fake osb client
// with success response
func (ct *controllerTest) SetSuccessfullyReactionForDeprovisioningToOSBClient() {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	ct.fakeOSBClient.DeprovisionReaction = fakeosb.DynamicDeprovisionReaction(
		func(_ *osb.DeprovisionRequest) (*osb.DeprovisionResponse, error) {
			return &osb.DeprovisionResponse{}, nil
		})
}

// EnsureBrokerActionExist makes sure specific fake osb client action exist
func (ct *controllerTest) EnsureBrokerActionExist(actionType fakeosb.ActionType) error {
	actions := ct.fakeOSBClient.Actions()
	for _, action := range actions {
		if action.Type == actionType {
			return nil
		}
	}

	return fmt.Errorf("expected broker action %q not exist", actionType)
}

// EnsureBrokerActionNotExist makes sure specific fake osb client action not exist
func (ct *controllerTest) EnsureBrokerActionNotExist(actionType fakeosb.ActionType) error {
	actions := ct.fakeOSBClient.Actions()
	for _, action := range actions {
		if action.Type == actionType {
			return fmt.Errorf("expected broker action %q exist", actionType)
		}
	}

	return nil
}

// EnsureLastUpdateBrokerActionHasCorrectPlan makes sure osb client action with type "UpdateInstance"
// contains specific plan ID in request body parameters
func (ct *controllerTest) EnsureLastUpdateBrokerActionHasCorrectPlan(planID string) error {
	actions := ct.fakeOSBClient.Actions()
	for _, action := range actions {
		if action.Type == fakeosb.UpdateInstance {
			request := action.Request.(*osb.UpdateInstanceRequest)
			if request.PlanID == nil {
				continue
			}
			if p, rp := planID, *request.PlanID; p != rp {
				continue
			}

			return nil
		}
	}

	return fmt.Errorf("expected ServicePlan %q not exist", planID)
}

// EnsureBrokerActionWithParametersExist makes sure osb client action with type "UpdateInstance"
// contains specific parameters in request body parameters
func (ct *controllerTest) EnsureBrokerActionWithParametersExist(parameters map[string]interface{}) error {
	actions := ct.fakeOSBClient.Actions()
	for _, action := range actions {
		if action.Type != fakeosb.UpdateInstance {
			continue
		}

		request := action.Request.(*osb.UpdateInstanceRequest)
		if !reflect.DeepEqual(request.Parameters, parameters) {
			return fmt.Errorf("unexpected parameters: expected %v, got %v", parameters, request.Parameters)
		}
	}

	return nil
}

// CreateSecretsForServiceInstanceWithSecretParams creates Secrets with specific parameters
func (ct *controllerTest) CreateSecretsForServiceInstanceWithSecretParams() error {
	_, err := ct.k8sClient.CoreV1().Secrets(testNamespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      secretNameWithParameters,
		},
		Data: map[string][]byte{
			secretKeyWithParameters: []byte(`{"secret-param-key":"secret-param-value"}`),
		},
	})
	if err != nil {
		return err
	}

	_, err = ct.k8sClient.CoreV1().Secrets(testNamespace).Create(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      otherSecretNameWithParameters,
		},
		Data: map[string][]byte{
			otherSecretKeyWithParameters: []byte(`{"other-secret-param-key":"other-secret-param-value"}`),
		},
	})

	return err
}

// SetSimpleErrorUpdateInstanceReaction sets up DynamicUpdateInstanceReaction for fake osb client
// which returns simple error response during three first call and success response after them
func (ct *controllerTest) SetSimpleErrorUpdateInstanceReaction() {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	numberOfPolls := 0
	numberOfInProgressResponses := 2

	ct.fakeOSBClient.UpdateInstanceReaction = fakeosb.DynamicUpdateInstanceReaction(
		func(_ *osb.UpdateInstanceRequest) (*osb.UpdateInstanceResponse, error) {
			numberOfPolls++
			if numberOfPolls > numberOfInProgressResponses {
				return &osb.UpdateInstanceResponse{}, nil
			}
			return nil, errors.New("fake update error")
		})
}

// SetErrorUpdateInstanceReaction sets up DynamicUpdateInstanceReaction for fake osb client
// which returns specific error response during three first call and success response after them
func (ct *controllerTest) SetErrorUpdateInstanceReaction() {
	ct.fakeOSBClient.Lock()
	defer ct.fakeOSBClient.Unlock()

	numberOfPolls := 0
	numberOfInProgressResponses := 2

	ct.fakeOSBClient.UpdateInstanceReaction = fakeosb.DynamicUpdateInstanceReaction(
		func(_ *osb.UpdateInstanceRequest) (*osb.UpdateInstanceResponse, error) {
			numberOfPolls++
			if numberOfPolls > numberOfInProgressResponses {
				return &osb.UpdateInstanceResponse{}, nil
			}
			return nil, osb.HTTPStatusCodeError{
				StatusCode:   http.StatusConflict,
				ErrorMessage: strPtr("OutOfQuota"),
				Description:  strPtr("You're out of quota!"),
			}
		})
}

// SetUpdateServiceInstanceResponseWithDashboardURL sets up UpdateInstanceReaction for fake osb client
// with specific url under the parameter `DashboardURL`
func (ct *controllerTest) SetUpdateServiceInstanceResponseWithDashboardURL() {
	dashURL := testDashboardURL
	ct.fakeOSBClient.UpdateInstanceReaction = &fakeosb.UpdateInstanceReaction{
		Response: &osb.UpdateInstanceResponse{
			DashboardURL: &dashURL,
		},
	}
}

// CheckFeatureGate enables/disables DefaultFeatureGate functionality and checks its behavior
func (ct *controllerTest) CheckFeatureGate(enableGate bool) error {
	instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting Instance: %v", err)
	}

	dashURL := testDashboardURL
	if enableGate {
		if utilfeature.DefaultFeatureGate.Enabled(scfeatures.UpdateDashboardURL) {
			if instance.Status.DashboardURL != &dashURL {
				return fmt.Errorf("unexpected DashboardURL: expected %v", dashURL)
			}
		}
	} else {
		if instance.Status.DashboardURL != nil {
			return fmt.Errorf("Dashboard URL should be nil")
		}
	}

	return nil
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
				Name:          testClusterServiceClassName,
				ID:            testClassExternalID,
				Description:   "a test service",
				Bindable:      true,
				PlanUpdatable: truePtr(),
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
					{
						Name:        testOtherClusterServicePlanName,
						Free:        truePtr(),
						ID:          testOtherPlanExternalID,
						Description: "an other test plan",
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

func fixtureUserInfo() *v1beta1.UserInfo {
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
