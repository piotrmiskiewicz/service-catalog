apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingWebhookConfiguration
metadata:
  name: mutating-webhook-example-cfg
  labels:
    app: admission-webhook-example
webhooks:
  - name: mutating-example.sap.com
    clientConfig:
      service:
        name: admission-webhook-example-svc
        namespace: default
        path: "/mutate"
      caBundle: CA_BUNDLE
    rules:
      - operations: [ "CREATE" ]
        apiGroups: ["servicecatalog.k8s.io"]
        apiVersions: ["v1beta1"]
        resources: ["clusterservicebrokers"]
    namespaceSelector:
      matchLabels:
        admission-webhook-example: enabled