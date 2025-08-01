.PHONY: apiserver client format gen test

all: client apiserver 

client:
	CGO_ENABLED=0 go build -p 3 -o bin/client ./client

apiserver:
	go build -p 3 -o bin/apiserver ./servers/apiserver

server-release:
	GOOS=linux GOARCH=amd64 go build -p 3 -o bin/apiserver-amd64 ./servers/apiserver
	GOOS=linux GOARCH=amd64 go build -p 3 -o bin/client-amd64 ./client

macclient:
	GOOS=darwin GOARCH=arm64 go build -p 3 -o bin/client_mac ./client

macclient-amd64:
	GOOS=darwin GOARCH=amd64 go build -p 3 -o bin/client_mac_amd64 ./client

format:
	go fmt ./...
	protolint lint --fix proto/*.proto

gen:
	go generate ./...
	(cd pkg/agent_runner/; go generate .)

test:
	go test ./pkg/broker/ ./pkg/db/sql/ ./pkg/auth ./pkg/agent ./pkg/storage

gce-test:
	GCE_BACKEND=skyworkstation/us-west1-a/instance-group-1 go test ./pkg/broker/backends/gce/

k8s-test:
	go test ./pkg/broker/backends/k8s/

tidy:
	go mod tidy
