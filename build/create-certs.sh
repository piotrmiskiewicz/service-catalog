#!/usr/bin/env bash

#! /bin/bash

WEBHOOK_NS="default"
WEBHOOK_SVC="admission-webhook-example-svc"

# Create certs for our webhook
openssl genrsa -out webhookCA.key 2048
openssl req -new -key ./webhookCA.key -subj "/CN=${WEBHOOK_SVC}.${WEBHOOK_NS}.svc" -out ./webhookCA.csr
openssl x509 -req -days 365 -in webhookCA.csr -signkey webhookCA.key -out webhook.crt

# Create certs secrets for k8s
kubectl create secret generic \
    ${WEBHOOK_SVC}-certs \
    --from-file=key.pem=./webhookCA.key \
    --from-file=cert.pem=./webhook.crt \
    --dry-run -o yaml > ./contrib/webhook/webhook-certs.yaml

# Set the CABundle on the webhook registration
CA_BUNDLE=$(cat ./webhook.crt | base64)
echo  $CA_BUNDLE
sed "s/CA_BUNDLE/${CA_BUNDLE}/" ./contrib/webhook/mwh.yaml.tpl > ./contrib/webhook/mwh.yaml

# Clean
rm ./webhookCA* && rm ./webhook.crt