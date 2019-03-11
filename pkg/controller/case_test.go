package controller_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	fakesc "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/fake"
	scinterface "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/typed/servicecatalog/v1beta1"
	scinformers "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/externalversions"
	"github.com/kubernetes-incubator/service-catalog/pkg/controller"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeosb "github.com/pmorie/go-open-service-broker-client/v2/fake"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"k8s.io/client-go/tools/record"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	v12 "k8s.io/api/core/v1"
	"github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	errors2 "k8s.io/apimachinery/pkg/api/errors"
	beta1 "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/externalversions/servicecatalog/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
	"net/http"
	k8sinformers "k8s.io/client-go/informers"
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

	pollingInterval = 50 * time.Millisecond
	pollingTimeout  = 8 * time.Second
)

type ControllerTest struct {
	// resource clientsets and interfaces
	scInterface                 scinterface.ServicecatalogV1beta1Interface
	k8sClient                   *fakek8s.Clientset
	fakeOSBClient               *fakeosb.FakeClient
	catalogReactions            []fakeosb.CatalogReaction
	osbClientCfg                *v2.ClientConfiguration
	stopCh                      chan struct{}
	plansInformer               beta1.ClusterServicePlanInformer
	clusterServiceClassInformer beta1.ClusterServiceClassInformer
}

func NewControllerTest(t *testing.T) *ControllerTest {
	k8sClient := fakek8s.NewSimpleClientset()
	k8sClient.CoreV1().Namespaces().Create(&v12.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
		},
	})

	fakeOSBClient := fakeosb.NewFakeClient(getTestHappyPathBrokerClientConfig())

	coreInformerFactory := k8sinformers.NewSharedInformerFactory(k8sClient, time.Minute)
	coreInformers := coreInformerFactory.Core()


	scClient := fakesc.NewSimpleClientset()
	informerFactory := scinformers.NewSharedInformerFactory(scClient, 0)
	serviceCatalogSharedInformers := informerFactory.Servicecatalog().V1beta1()

	clusterServiceClassInformer := serviceCatalogSharedInformers.ClusterServiceClasses()
	plansInformer := serviceCatalogSharedInformers.ClusterServicePlans()

	testCase := &ControllerTest{
		scInterface:                 scClient.ServicecatalogV1beta1(),
		k8sClient:                   k8sClient,
		fakeOSBClient:               fakeOSBClient,
		catalogReactions:            []fakeosb.CatalogReaction{},
		clusterServiceClassInformer: clusterServiceClassInformer,
		plansInformer:               plansInformer,
	}

	// wrap the ClientFunc with a helper which saves last used OSG Client Config (it can be asserted in the test)
	brokerClFunc := testCase.spyOSBClientFunc(fakeosb.ReturnFakeClientFunc(fakeOSBClient))

	fakeRecorder := record.NewFakeRecorder(1)
	// start goroutine which flushes events (prevent hanging recording function)
	go func() {
		for  range fakeRecorder.Events {
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
		24*time.Hour,
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

	// start the controller
	testCase.stopCh = make(chan struct{})
	informerFactory.Start(testCase.stopCh)
	coreInformerFactory.Start(testCase.stopCh)
	informerFactory.WaitForCacheSync(testCase.stopCh)
	coreInformerFactory.WaitForCacheSync(testCase.stopCh)
	go testController.Run(1, testCase.stopCh)

	return testCase
}

func (ct *ControllerTest) TearDown() {
	close(ct.stopCh)
}

func (ct *ControllerTest) AsyncForInstances() {
	ct.fakeOSBClient.ProvisionReaction.(*fakeosb.ProvisionReaction).Response.Async = true
	ct.fakeOSBClient.UpdateInstanceReaction.(*fakeosb.UpdateInstanceReaction).Response.Async = true
	ct.fakeOSBClient.DeprovisionReaction.(*fakeosb.DeprovisionReaction).Response.Async = true
}

func (ct *ControllerTest) AsyncForBindings() {
	ct.fakeOSBClient.BindReaction.(*fakeosb.BindReaction).Response.Async = true
	ct.fakeOSBClient.UnbindReaction.(*fakeosb.UnbindReaction).Response.Async = true
}

func (ct *ControllerTest) AssertOSBBasicAuth(t *testing.T, username, password string) {
	require.NotNil(t, ct.osbClientCfg, "OSB Client was not created, wait for broker is ready")
	assert.Equal(t, ct.osbClientCfg.AuthConfig.BasicAuthConfig, &v2.BasicAuthConfig{
		Username: username,
		Password: password,
	})
}

func (ct *ControllerTest) SetOSBPollLastOperationReactionInProgress(numberOfInProgressResponses int) {
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

func (ct *ControllerTest) SetOSBProvisionReactionHTTPError(numberOfErrorResponses int) {
	numberOfPolls := 0
	ct.fakeOSBClient.ProvisionReaction = fakeosb.DynamicProvisionReaction(
		func(_ *osb.ProvisionRequest) (*osb.ProvisionResponse, error) {
			numberOfPolls++
			if numberOfPolls > numberOfErrorResponses {
				return &osb.ProvisionResponse{}, nil
			}
			return nil, osb.HTTPStatusCodeError{
				StatusCode:   http.StatusUnauthorized,
			}
		})
}

func (ct *ControllerTest) SetOSBBindReactionWithHTTPError(code int) {
	ct.fakeOSBClient.BindReaction = &fakeosb.BindReaction{
		Error: osb.HTTPStatusCodeError{
			StatusCode: code,
		},
	}
}

func (ct *ControllerTest) spyOSBClientFunc(target v2.CreateFunc) v2.CreateFunc {
	return func(osbCfg *v2.ClientConfiguration) (v2.Client, error) {
		ct.osbClientCfg = osbCfg
		return target(osbCfg)
	}
}

func (ct *ControllerTest) createClusterServiceBroker() *v1beta1.ClusterServiceBroker {
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

func (ct *ControllerTest) CreateSimpleClusterServiceBroker() {
	ct.scInterface.ClusterServiceBrokers().Create(ct.createClusterServiceBroker())
}

func (ct *ControllerTest) CreateClusterServiceBrokerWithBasicAuth() {
	csb := ct.createClusterServiceBroker()
	csb.Spec.AuthInfo = &v1beta1.ClusterServiceBrokerAuthInfo{
		Basic: &v1beta1.ClusterBasicAuthConfig{
			SecretRef: &v1beta1.ObjectReference{
				Name:      authSecretName,
				Namespace: testNamespace,
			},
		},
	}
	ct.scInterface.ClusterServiceBrokers().Create(csb)
}

func (ct *ControllerTest) CreateServiceInstance() {
	ct.scInterface.ServiceInstances(testNamespace).Create(&v1beta1.ServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: testServiceInstanceName,
		},
		Spec: v1beta1.ServiceInstanceSpec{
			PlanReference: v1beta1.PlanReference{
				ClusterServiceClassExternalName: testClassExternalID,
				ClusterServicePlanExternalName:  testPlanExternalID,
			},
			ExternalID: testExternalID,
			// the Plan/Class refs are added by a Webhook, which is not tested here
			ClusterServicePlanRef: &v1beta1.ClusterObjectReference{
				Name: testPlanExternalID,
			},
			ClusterServiceClassRef: &v1beta1.ClusterObjectReference{
				Name: testClassExternalID,
			},
		},
	})
}

func (ct *ControllerTest) DeleteServiceInstance() {
	ct.scInterface.ServiceInstances(testNamespace).Delete(testServiceInstanceName, &v1.DeleteOptions{})
}

func (ct *ControllerTest) CreateBinding() {
	ct.scInterface.ServiceBindings(testNamespace).Create(&v1beta1.ServiceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  testNamespace,
			Name:       testBindingName,
			Generation: 1,                                         // set by the Webhook
			Finalizers: []string{v1beta1.FinalizerServiceCatalog}, // set by the Webhook
		},
		Spec: v1beta1.ServiceBindingSpec{
			InstanceRef: v1beta1.LocalObjectReference{
				Name: testServiceInstanceName,
			},
			ExternalID: testServiceBindingGUID,
			SecretName: testBindingName, // set by the webhook
		},
	})
}

func (ct *ControllerTest) DeleteBinding() {
	ct.scInterface.ServiceBindings(testNamespace).Delete(testBindingName, &v1.DeleteOptions{})
}

func (ct *ControllerTest) CreateSecretWithBasicAuth(username, password string) {
	ct.k8sClient.CoreV1().Secrets(testNamespace).Create(&v12.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      authSecretName,
		},
		Data: map[string][]byte{
			v1beta1.BasicAuthUsernameKey: []byte(username),
			v1beta1.BasicAuthPasswordKey: []byte(password),
		},
	})
}

func (ct *ControllerTest) UpdateSecretWithBasicAuth(username, password string) {
	ct.k8sClient.CoreV1().Secrets(testNamespace).Update(&v12.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      authSecretName,
		},
		Data: map[string][]byte{
			v1beta1.BasicAuthUsernameKey: []byte(username),
			v1beta1.BasicAuthPasswordKey: []byte(password),
		},
	})
}

func (ct *ControllerTest) AssertClusterServiceClassAndPlan(t *testing.T) {
	err := ct.WaitForClusterServiceClass()
	if err != nil {
		t.Fatal(err)
	}

	err = ct.WaitForClusterServicePlan()
	if err != nil {
		t.Fatal(err)
	}
}

func (ct *ControllerTest) SetCatalogReactionError() {
	ct.fakeOSBClient.CatalogReaction = &fakeosb.CatalogReaction{
		Error: errors.New("ooops"),
	}
}

func (ct *ControllerTest) WaitForReadyBinding() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionTrue,
	})
}

func (ct *ControllerTest) WaitForNotReadyBinding() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionFalse,
	})
}

func (ct *ControllerTest) WaitForBindingOrphanMitigationSuccessful() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionReady,
		Status: v1beta1.ConditionFalse,
		Reason: "OrphanMitigationSuccessful",
	})
}

func (ct *ControllerTest) WaitForBindingFailed() error {
	return ct.waitForBindingStatusCondition(v1beta1.ServiceBindingCondition{
		Type:   v1beta1.ServiceBindingConditionFailed,
		Status: v1beta1.ConditionTrue,
	})
}

func (ct *ControllerTest) waitForBindingStatusCondition(condition v1beta1.ServiceBindingCondition) error {
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

func (ct *ControllerTest) WaitForServiceInstanceRemoved() error {
	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if errors2.IsNotFound(err) {
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

func (ct *ControllerTest) WaitForReadyInstance() error {
	condition := v1beta1.ServiceInstanceCondition{
		Type:   v1beta1.ServiceInstanceConditionReady,
		Status: v1beta1.ConditionTrue,
	}

	var lastInstance *v1beta1.ServiceInstance
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		instance, err := ct.scInterface.ServiceInstances(testNamespace).Get(testServiceInstanceName, v1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting Broker: %v", err)
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
		return fmt.Errorf("the instance is not ready, current status: %+v", lastInstance.Status)
	}
	return err
}

func (ct *ControllerTest) WaitForReadyBroker() error {
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

func (ct *ControllerTest) WaitForClusterServiceClass() error {
	return wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		_, err := ct.clusterServiceClassInformer.Lister().Get(testClassExternalID)
		if err == nil {
			return true, nil
		}

		return false, err
	})
}

func (ct *ControllerTest) WaitForClusterServicePlan() error {
	err := wait.PollImmediate(pollingInterval, pollingTimeout, func() (bool, error) {
		_, err := ct.plansInformer.Lister().Get(testPlanExternalID)
		if err == nil {
			return true, nil
		}

		return false, err
	})
	if err == wait.ErrWaitTimeout {
		plans, e := ct.plansInformer.Lister().List(labels.Everything())
		if e != nil {
			return err
		}
		return fmt.Errorf("plan %v not found, existing plans: %v", testPlanExternalID, plans)
	}
	return err
}

//func (ct *ControllerTest) lastBrokerAction() fakeosb.Action {
//	ct.fakeOSBClient.Actions()
//}

func getTestHappyPathBrokerClientConfig() fakeosb.FakeClientConfiguration {
	return fakeosb.FakeClientConfiguration{
		CatalogReaction: &fakeosb.CatalogReaction{
			Response: getTestCatalogResponse(),
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
				Credentials: getTestBindCredentials(),
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
				Credentials: getTestBindCredentials(),
			},
		},
	}
}

// getTestCatalogResponse returns a sample response to a get catalog request.
func getTestCatalogResponse() *osb.CatalogResponse {
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

// getTestBindCredentials returns binding credentials to include in the response
// to a bind request.
func getTestBindCredentials() map[string]interface{} {
	return map[string]interface{}{
		"foo": "bar",
		"baz": "zap",
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
