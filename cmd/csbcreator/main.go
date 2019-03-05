package main

import (
	"github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset"
	"k8s.io/client-go/tools/clientcmd"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	"os"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
)

var runtimeScheme = runtime.NewScheme()

func init() {

	_ = v1beta1.AddToScheme(runtimeScheme)

}

func main() {
	k8sConfig, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		panic(err)
	}
	c, err := clientset.NewForConfig(k8sConfig)
	if err != nil {
		panic(err)
	}
	toCreate := &v1beta1.ClusterServiceBroker{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testing-csb",
		},
		Spec: v1beta1.ClusterServiceBrokerSpec{
			CommonServiceBrokerSpec: v1beta1.CommonServiceBrokerSpec{
				URL: "http://local.svc",
			},
		},
	}
	//v1beta1.SetDefaults_ClusterServiceBrokerSpec(&toCreate.Spec)
	//fmt.Printf("before %+v\n", toCreate.Spec.RelistBehavior)
	//runtimeScheme.Default(toCreate)
	//fmt.Printf("after %+v\n", toCreate.Spec.RelistBehavior)
	csb, err := c.ServicecatalogV1beta1().ClusterServiceBrokers().Create(toCreate)


	if err != nil {
		panic(err)
	}

	fmt.Println("Returned CSB:\n %+v", csb)
	fmt.Println("\t%v", csb.Spec.RelistBehavior)


}
