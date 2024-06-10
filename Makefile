.DEFAULT_GOAL := build

VERSION ?= $(shell ./hack/get-version.sh)

GO_IMG ?= golang:alpine3.20@sha256:1b455a3f7786e5765dbeb4f7ab32a36cdc0c3f4ddd35406606df612dc6e3269b

.PHONY: fmt vet build unit-test

DOCKER_CMD = docker run --rm -v $(PWD):/app -w /app ${GO_IMG}

## verify: Format code
fmt:
	go fmt ./...

## verify: Verify code
vet: fmt
	go vet ./...

## build: Build binary
build: vet
	go build

## unit-test: Run unit tests
unit-test:
ifeq ($(USE_DOCKER), 1)
	@${DOCKER_CMD} go test -v ./...
else
	go test -v ./... ;
endif

## build-image: Build image
build-docker: vet
	docker build -t numberly/vault-db-injector:${VERSION} .

## build-image: Push image
push-docker: build-docker
	docker push numberly/vault-db-injector:${VERSION}
