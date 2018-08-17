eval (minikube docker-env)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o servicebroker-linux --ldflags="-s" .
docker build -t "nasty-app:1.0.0" .