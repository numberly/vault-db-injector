.DEFAULT_GOAL := build

VERSION ?= $(shell ./hack/get-version.sh)

.PHONY:fmt vet build
fmt:
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	go build

build-docker: vet
	docker build -t numberly/vault-db-injector:${VERSION} .

push-docker: build-docker
	docker push numberly/vault-db-injector:${VERSION}
