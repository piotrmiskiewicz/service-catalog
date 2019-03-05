package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"fmt"
	"encoding/json"
	"io/ioutil"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"

	scv1beta1 "github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func main() {

	whsvr := &WebhookServer{
		server: &http.Server{
			Addr:      ":8080",
		},
	}

	// define http server and server handler
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", whsvr.serve)
	whsvr.server.Handler = mux

	// start webhook server in new routine
	go func() {
		if err := whsvr.server.ListenAndServeTLS("/etc/webhook/certs/cert.pem", "/etc/webhook/certs/key.pem"); err != nil {
			glog.Errorf("Failed to listen and serve webhook server: %v", err)
		}
	}()

	glog.Info("Server started")

	// listening OS shutdown singal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	glog.Infof("Got OS shutdown signal, shutting down webhook server gracefully...")
	whsvr.server.Shutdown(context.Background())
}


var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	// (https://github.com/kubernetes/kubernetes/issues/57982)
	defaulter = runtime.ObjectDefaulter(runtimeScheme)
)


type WebhookServer struct {
	server *http.Server
}

// Webhook Server parameters
type WhSvrParameters struct {
	port           int    // webhook server port
	certFile       string // path to the x509 certificate for https
	keyFile        string // path to the x509 private key matching `CertFile`
	sidecarCfgFile string // path to sidecar injector configuration file
}


func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1beta1.AddToScheme(runtimeScheme)
	e := scv1beta1.AddToScheme(runtimeScheme)
	fmt.Println(e)
}

// validate deployments and services
func (whsvr *WebhookServer) validate(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{
		Allowed: true,
	}
}

// main mutation process
func (whsvr *WebhookServer) mutate(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	req := ar.Request

	obj, err := runtimeScheme.New(schema.GroupVersionKind{
		Group: req.Kind.Group,
		Kind: req.Kind.Kind,
		Version: req.Kind.Version,
	})
	if err != nil {
		glog.Errorf("Could not create obj: %v", err)
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}
	glog.Infof("Object %+v", obj)

	defaulter.Default(obj)

	glog.Infof("After defaults applied to gvk: %+v", obj)

	return &v1beta1.AdmissionResponse{
		Allowed: true,
	}
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		glog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		glog.Errorf("Can't decode body: %v", err)
		admissionResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		fmt.Println(r.URL.Path)
		if r.URL.Path == "/mutate" {
			admissionResponse = whsvr.mutate(&ar)
		} else if r.URL.Path == "/validate" {
			admissionResponse = whsvr.validate(&ar)
		}
	}

	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		glog.Errorf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	glog.Infof("Ready to write reponse ...")
	if _, err := w.Write(resp); err != nil {
		glog.Errorf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}





// from apiserver/pkg/admission/plugin/webhook/generic
// convertor converts objects to the desired version.
// ConvertToGVK converts object to the desired gvk.
func ConvertToGVK(obj runtime.Object, gvk schema.GroupVersionKind) (runtime.Object, error) {
	// Unlike other resources, custom resources do not have internal version, so
	// if obj is a custom resource, it should not need conversion.
	//if obj.GetObjectKind().GroupVersionKind() == gvk {
	//	return obj, nil
	//}
	out, err := runtimeScheme.New(gvk)
	if err != nil {
		return nil, err
	}
	err = runtimeScheme.Convert(obj, out, nil)
	if err != nil {
		return nil, err
	}
	return out, nil
}
