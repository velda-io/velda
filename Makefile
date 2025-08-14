.PHONY: apiserver client format gen test unittest

all: client apiserver 

client:
	CGO_ENABLED=0 go build -p 3 -o bin/velda ./client

apiserver:
	go build -p 3 -o bin/apiserver ./servers/apiserver

TAGS ?= k8s,gce,gcs_provisioner,aws
release:
	GOOS=linux GOARCH=amd64 go build --tags "${TAGS}" -p 3 -o bin/apiserver-${VERSION}-linux-amd64 ./servers/apiserver
	GOOS=linux GOARCH=amd64 go build -p 3 -o bin/velda-${VERSION}-linux-amd64 ./client
	GOOS=linux GOARCH=arm64 go build --tags "${TAGS}" -p 3 -o bin/apiserver-${VERSION}-linux-arm64 ./servers/apiserver
	GOOS=linux GOARCH=arm64 go build -p 3 -o bin/velda-${VERSION}-linux-arm64 ./client
	GOOS=darwin GOARCH=arm64 go build -p 3 -o bin/velda-${VERSION}-darwin-arm64 ./client
	GOOS=darwin GOARCH=amd64 go build -p 3 -o bin/velda-${VERSION}-darwin-amd64 ./client

format:
	go fmt ./...
	protolint lint --fix proto/*.proto

gen:
	go generate ./...
	(cd pkg/agent_runner/; go generate .)

unittest:
	go test ./pkg/broker/ ./pkg/agent

test: unittest
	go test ./tests --tags local

tidy:
	go mod tidy
