.PHONY: apiserver client format gen test unittest

all: client apiserver 

client:
	CGO_ENABLED=0 go build -p 3 -o bin/velda ./client

apiserver:
	go build -p 3 -o bin/apiserver ./servers/apiserver

server-release:
	GOOS=linux GOARCH=amd64 go build -p 3 -o bin/apiserver-amd64 ./servers/apiserver
	GOOS=linux GOARCH=amd64 go build -p 3 -o bin/velda-amd64 ./client

macclient:
	GOOS=darwin GOARCH=arm64 go build -p 3 -o bin/velda-mac-arm64 ./client

macclient-amd64:
	GOOS=darwin GOARCH=amd64 go build -p 3 -o bin/velda-mac-amd64 ./client

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
