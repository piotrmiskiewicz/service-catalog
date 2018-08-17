CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/service-catalog ./cmd/service-catalog

export IMAGE_NAME=service-catalog:0.1.28-fix

docker build -t ${IMAGE_NAME} -f build/service-catalog/Dockerfile .

docker tag ${IMAGE_NAME} repository.hybris.com:5003/gopher/${IMAGE_NAME}

docker push repository.hybris.com:5003/gopher/${IMAGE_NAME}