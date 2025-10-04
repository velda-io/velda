.PHONY: velda format gen test unittest

all: velda

# By default, disabled grpctrace & k8s backend to reduce binary size
# See https://github.com/golang/go/issues/62024
TAGS ?= gce,aws,grpcnotrace
VERSION ?= dev-$(shell date +%Y%m%d-%H%M%S)
VERSION_V := ${VERSION}
velda:
	CGO_ENABLED=0 go build --tags "${TAGS}" -p 3 -o bin/velda ./client

debug-deps:
	CGO_ENABLED=0 go build -ldflags='-dumpdep' --tags "${TAGS}" -p 3 -o bin/velda ./client >bin/velda-deps 2>&1

RELEASE_FLAGS = -p $(shell nproc) --tags "${TAGS}" -ldflags "-X velda.io/velda.Version=${VERSION_V}"
release-mini:
	GOOS=linux GOARCH=amd64 go build ${RELEASE_FLAGS} -o bin/velda-${VERSION_V}-linux-amd64 ./client

release:
	GOOS=linux GOARCH=amd64 go build ${RELEASE_FLAGS} -o bin/velda-${VERSION_V}-linux-amd64 ./client
	GOOS=linux GOARCH=arm64 go build ${RELEASE_FLAGS} -o bin/velda-${VERSION_V}-linux-arm64 ./client
	GOOS=darwin GOARCH=arm64 go build ${RELEASE_FLAGS} -o bin/velda-${VERSION_V}-darwin-arm64 ./client
	GOOS=darwin GOARCH=amd64 go build ${RELEASE_FLAGS} -o bin/velda-${VERSION_V}-darwin-amd64 ./client

format:
	go fmt ./...
	protolint lint --fix proto/*.proto

gen:
	go generate ./...
	(cd pkg/agent_runner/; go generate .)

unittest:
	go test ./pkg/... ./client/...

e2etest: velda
	go test ./tests --tags local -v

test: unittest e2etest

tidy:
	go mod tidy

PACKER_FILTER ?= *

image: release-mini
	packer build -var="version=${VERSION_V}" -only="${PACKER_FILTER}" packer

manifest: pkg/broker/backends/k8s/k8s_provisioner.go
	controller-gen crd:generateEmbeddedObjectMeta=true paths=./pkg/broker/backends/k8s/ output:crd:dir=./misc