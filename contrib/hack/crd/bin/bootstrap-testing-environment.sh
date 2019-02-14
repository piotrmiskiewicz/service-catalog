#!/usr/bin/env bash

CURRENT_DIR=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )

echo "- Initialize Minikube"
bash ${CURRENT_DIR}/minikube.sh

echo "- Installing Tiller..."
kubectl apply -f ${CURRENT_DIR}/../assets/tiller.yaml

bash ${CURRENT_DIR}/is-ready.sh kube-system name tiller

echo "- Register Service Catalog CRDs"
kubectl apply -f  ${CURRENT_DIR}/../assets/svc-crds.yaml

echo "- Installing Helm Broker Helm Chart"
helm install ${CURRENT_DIR}/../assets/helm-broker-chart.tgz  --name helm-broker --namespace kyma-system --wait
echo "- Installing BUC Helm Chart"
helm install ${CURRENT_DIR}/../assets/buc-chart.tgz  --name buc --namespace kyma-system --wait

echo "- Register Helm Broker in Service Catalog"
kubectl apply -f  ${CURRENT_DIR}/../assets/helm-broker.yaml

echo "- Expose Helm Broker to localhost on port 8081"
export HB_POD_NAME=$(kubectl get po -l app=helm-broker -n kyma-system -o jsonpath='{ .items[*].metadata.name }')
kubectl port-forward -n kyma-system pod/${HB_POD_NAME} 8081:8080