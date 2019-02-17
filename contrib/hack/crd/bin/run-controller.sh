#!/usr/bin/env bash

readonly ROOT_PATH=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )

go run ${ROOT_PATH}/../../../../cmd/service-catalog/main.go controller-manager \
--secure-port="8444" \
--cluster-id-configmap-namespace="default" \
--leader-elect="false" \
-v="6" \
--resync-interval="5m" \
--broker-relist-interval="24h" \
--operation-polling-maximum-backoff-duration="20m" \
--k8s-kubeconfig="${KUBECONFIG}" \
--service-catalog-kubeconfig="${KUBECONFIG}" \
--cert-dir="${ROOT_PATH}/../../../../tmp/" \
--feature-gates="OriginatingIdentity=true" \
--feature-gates="ServicePlanDefaults=false"