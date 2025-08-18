.PHONY: velda format gen test unittest

all: velda

TAGS ?= k8s,gce,gcs_provisioner,aws
VERSION ?= dev
velda:
	CGO_ENABLED=0 go build --tags "${TAGS}" -p 3 -o bin/velda ./client

release-mini:
	GOOS=linux GOARCH=amd64 go build -p 3 --tags "${TAGS}" -o bin/velda-${VERSION}-linux-amd64 ./client

release:
	GOOS=linux GOARCH=amd64 go build -p 3 --tags "${TAGS}" -o bin/velda-${VERSION}-linux-amd64 ./client
	GOOS=linux GOARCH=arm64 go build -p 3 --tags "${TAGS}" -o bin/velda-${VERSION}-linux-arm64 ./client
	GOOS=darwin GOARCH=arm64 go build -p 3 --tags "${TAGS}" -o bin/velda-${VERSION}-darwin-arm64 ./client
	GOOS=darwin GOARCH=amd64 go build -p 3 --tags "${TAGS}" -o bin/velda-${VERSION}-darwin-amd64 ./client

format:
	go fmt ./...
	protolint lint --fix proto/*.proto

gen:
	go generate ./...
	(cd pkg/agent_runner/; go generate .)

unittest:
	go test ./pkg/... ./client/...

e2etest:
	go test ./tests --tags local

test: unittest e2etest

tidy:
	go mod tidy

images:
	$(MAKE) -C packer