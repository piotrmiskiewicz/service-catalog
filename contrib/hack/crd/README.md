## Cookbook for CRDs POC 

Execute all commands from the cookbook in the `hack` directory.

### Prerequisites

Kyma installed on your cluster but without the ServiceCatalog.

### Steps to run controller locally

1. Register the Service Catalog CRDs
```bash
kubectl apply -f ./assets/svc-crds.yaml
```

2. Register Helm Broker
```bash
kubectl apply -f ./assets/helm-broker.yaml
```

3. Export the name of the HelmBroker Pod.
```bash
export HB_POD_NAME=$(kubectl get po -l app=helm-broker -n kyma-system -o jsonpath='{ .items[*].metadata.name }')
```

4. Expose helm-broker service
```bash
kubectl port-forward -n kyma-system pod/${HB_POD_NAME} 8081:8080
```

5. Run the Service Catalog controller-manager
```bash
./bin/run-controller.sh
```

### Testing Scenario

Follow these steps:

1. Export the name of the Namespace.
```bash
export namespace="qa"
```
2. Create a Redis instance.
```bash
kubectl create -f assets/scenario/redis-instance.yaml -n $namespace
```
3. Check if the Redis instance is already provisioned.
```bash
watch -n 1 kubectl get serviceinstance/redis -n $namespace -o jsonpath='{ .status.conditions[0].reason }'
```
4. Create Secrets for the Redis instance.
```bash
kubectl create -f assets/scenario/redis-instance-binding.yaml -n $namespace
```
5. Create a lambda.
```bash
kubectl create -f assets/scenario/redis-client.yaml -n $namespace
```
6. Create a Binding Usage with **APP_** prefix.
```bash
kubectl create -f assets/scenario/service-binding-usage.yaml -n $namespace
```
7. Wait until the Function is ready.
```bash
kubeless function ls redis-client --namespace $namespace
```
9. Trigger the Function.
```bash
kubeless function call redis-client --namespace $namespace
```

The information and statistics about the Redis server appear.


### Documentation

- [Design of the Service Catalog](https://svc-cat.io/docs/design/)
- [Service Catalog Developer Guide](https://svc-cat.io/docs/devguide/)
- [Service Catalog Code & Documentation Standards](https://svc-cat.io/docs/code-standards/)