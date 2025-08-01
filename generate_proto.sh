#!/bin/bash
protoc -I proto --go_out=. --go-grpc_out=. --grpc-gateway_out=. --go_opt=module=velda.io/velda --go-grpc_opt=module=velda.io/velda --grpc-gateway_opt=module=velda.io/velda proto/*.proto proto/*/*.proto
