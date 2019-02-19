package controller

import (
	"testing"
	fakeosb "github.com/pmorie/go-open-service-broker-client/v2/fake"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"fmt"
	scFake "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/fake"
	"k8s.io/client-go/tools/record"
	clientgofake "k8s.io/client-go/kubernetes/fake"
	servicecataloginformers "github.com/kubernetes-incubator/service-catalog/pkg/client/informers_generated/externalversions"
)

const (
	testClassExternalID                   = "12345"
	testPlanExternalID                    = "34567"
	testNonbindablePlanExternalID         = "nb34567"
)

func TestBasicFlow(t *testing.T) {
	// GIVEN
	scClient := scFake.NewSimpleClientset()
	createAndStartController(t, getTestHappyPathBrokerClientConfig(), scClient)

	// WHEN

	// Step 1: create a ClusterServiceBroker
	scClient.ServicecatalogV1beta1().ClusterServiceBrokers().Create(getTestClusterServiceBroker())
	time.Sleep(time.Second)

	// THEN
	// expected one class
	classList, _ := scClient.ServicecatalogV1beta1().ClusterServiceClasses().List(v1.ListOptions{})
	fmt.Println(classList.Items[0].Name)

	// WHEN
	// Step 2: create an instance
	sc := getTestServiceInstance()
	sc.Namespace = "default"
	scClient.ServicecatalogV1beta1().ServiceInstances("default").Create(sc)
	time.Sleep(time.Second)

	// THEN
	// expected status "Ready"
	gotSC, _ := scClient.ServicecatalogV1beta1().ServiceInstances("default").Get(sc.Name, v1.GetOptions{})
	fmt.Printf("%+v\n", gotSC.Status)
}

// getTestHappyPathBrokerClientConfig returns configuration for the fake
// broker client that is appropriate for testing the synchronous happy path.
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

func createAndStartController(t *testing.T, config fakeosb.FakeClientConfiguration, fakeCatalogClient *scFake.Clientset)  {
	// create a fake kube client
	fakeKubeClient := &clientgofake.Clientset{}

	fakeOSBClient := fakeosb.NewFakeClient(config) // error should always be nil
	brokerClFunc := fakeosb.ReturnFakeClientFunc(fakeOSBClient)

	// create informers
	informerFactory := servicecataloginformers.NewSharedInformerFactory(fakeCatalogClient, 0)
	serviceCatalogSharedInformers := informerFactory.Servicecatalog().V1beta1()

	fakeRecorder := record.NewFakeRecorder(5)

	// create a test controller
	testController, err := NewController(
		fakeKubeClient,
		fakeCatalogClient.ServicecatalogV1beta1(),
		serviceCatalogSharedInformers.ClusterServiceBrokers(),
		serviceCatalogSharedInformers.ServiceBrokers(),
		serviceCatalogSharedInformers.ClusterServiceClasses(),
		serviceCatalogSharedInformers.ServiceClasses(),
		serviceCatalogSharedInformers.ServiceInstances(),
		serviceCatalogSharedInformers.ServiceBindings(),
		serviceCatalogSharedInformers.ClusterServicePlans(),
		serviceCatalogSharedInformers.ServicePlans(),
		brokerClFunc,
		24*time.Hour,
		osb.LatestAPIVersion().HeaderValue(),
		fakeRecorder,
		7*24*time.Hour,
		7*24*time.Hour,
		DefaultClusterIDConfigMapName,
		DefaultClusterIDConfigMapNamespace,
	)

	if err != nil {
		t.Fatal(err)
	}

	if c, ok := testController.(*controller); ok {
		c.setClusterID(testClusterID)
		c.brokerClientManager.clients[NewClusterServiceBrokerKey(getTestClusterServiceBroker().Name)] = clientWithConfig{
			OSBClient: fakeOSBClient,
		}
		c.brokerClientManager.clients[NewServiceBrokerKey(getTestServiceBroker().Namespace, getTestServiceBroker().Name)] = clientWithConfig{
			OSBClient: fakeOSBClient,
		}
	}

	//fakeKubeClient.AddReactor("get", "namespaces", func(action clientgotesting.Action) (bool, runtime.Object, error) {
	//	return true, &corev1.Namespace{
	//		ObjectMeta: metav1.ObjectMeta{
	//			Name: testNamespace,
	//			UID:  testNamespaceGUID,
	//		},
	//	}, nil
	//})

	stopCh := make(<-chan struct{})
	informerFactory.Start(stopCh)
	go testController.Run(1, stopCh)
}
