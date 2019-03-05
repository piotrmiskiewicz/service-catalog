#!/usr/bin/env bash

CGO_ENABLED=0 GOOS=linux go build -o wh cmd/webhook/main.go
eval `minikube docker-env`
docker build -t webhook-def -f contrib/Dockerfile .


rm wh
