## Cookbook for API Server to CRDs migration 

### Bootstrap local environment with Service Catalog with API Server for testing

1. In one shell execute:
```bash
./bin/bootstrap-migration-environment.sh
```

Under the hood this script is:
- creating minikube
- installing tiller
- installing Service Catalog with API Server
- installing Helm Broker

**Now you are ready to go!**

When you execute `svcat get classes`, then you should see:
```bash
          NAME           NAMESPACE                 DESCRIPTION
+----------------------+-----------+------------------------------------------+
  azure-service-broker               Extends the Service Catalog with Azure
                                     services
  redis                              Redis by Helm Broker (Experimental)
  gcp-service-broker                 Extends the Service Catalog with Google
                                     Cloud Platform services
``` 

### Prepare Service Catalog resources

Follow these steps:

1. Export the name of the Namespace.
```bash
export namespace="qa"
```
2. Create a Redis instance.
```bash
kubectl create -f assets/scenario/redis-instance-manual.yaml -n $namespace
```
3. Check if the Redis instance is already provisioned.
```bash
watch -n 1 "kubectl get serviceinstance/redis -n $namespace -o jsonpath='{ .status.conditions[0].reason }'"
```
4. Create Secrets for the Redis instance.
```bash
kubectl create -f assets/scenario/redis-instance-binding-manual.yaml -n $namespace
```
5. Create a deploy.
```bash
kubectl create -f assets/scenario/redis-client.yaml -n $namespace
```
6. Create a Binding Usage with **APP_** prefix.
```bash
kubectl create -f assets/scenario/service-binding-usage.yaml -n $namespace
```
7. Wait until the Pod is ready.
```bash
kubectl get po -l app=redis-client -n $namespace -o jsonpath='{ .items[*].status.conditions[?(@.type=="Ready")].status }'
```

### Backup

```bash
kubectl get serviceinstance -o yaml --export --all-namespaces > serviceinstance.yaml
kubectl get servicebinding -o yaml --export --all-namespaces > servicebinding.yaml
kubectl get clusterserviceclass -o yaml --export --all-namespaces > clusterserviceclass.yaml
kubectl get clusterserviceplan -o yaml --export --all-namespaces > clusterserviceplan.yaml
kubectl get clusterservicebroker -o yaml --export --all-namespaces > clusterservicebroker.yaml
kubectl get serviceclass -o yaml --export --all-namespaces > serviceclass.yaml
kubectl get serviceplan -o yaml --export --all-namespaces > serviceplan.yaml
kubectl get servicebroker -o yaml --export --all-namespaces > servicebroker.yaml
```

### Delete old SC

```bash
helm delete catalog --purge
```

### Install new SC

```bash
helm install ../../../charts/catalog --name catalog --namespace kyma-system
```

and scale down the controller manager

```bash
kubectl -n kyma-system scale deploy --replicas=0 catalog-catalog-controller-manager
```

### Import

```bash
kubectl apply -f serviceinstance.yaml
kubectl apply -f servicebinding.yaml
kubectl apply -f clusterserviceclass.yaml
kubectl apply -f clusterserviceplan.yaml
kubectl apply -f clusterservicebroker.yaml
kubectl apply -f serviceclass.yaml
kubectl apply -f serviceplan.yaml
kubectl apply -f servicebroker.yaml
```

scale up:

```bash
kubectl -n kyma-system scale deploy --replicas=1 catalog-catalog-controller-manager
 ```

8. Export the name of the Pod.
```bash
export POD_NAME=$(kubectl get po -l app=redis-client -n $namespace -o jsonpath='{ .items[*].metadata.name }')
```
9. Execute the `check-redis` script on the Pod.
```bash
kubectl exec ${POD_NAME} -n $namespace /check-redis.sh
```

The information and statistics about the Redis server appear.

