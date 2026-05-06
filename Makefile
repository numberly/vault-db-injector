.DEFAULT_GOAL := build

VERSION ?= $(shell ./hack/get-version.sh)

GO_IMG ?= golang:alpine3.20@sha256:1b455a3f7786e5765dbeb4f7ab32a36cdc0c3f4ddd35406606df612dc6e3269b

.PHONY: fmt vet build unit-test integration-test helm-docs helm-docs-check

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

## integration-test: Run integration tests (requires vault binary or Docker)
integration-test:
	go test -tags=integration -v ./...

## build-image: Build image
build-docker: vet
	docker build -t numberly/vault-db-injector:${VERSION} .

## build-image: Push image
push-docker: build-docker
	docker push numberly/vault-db-injector:${VERSION}

## helm-docs: Regenerate helm/README.md from helm/values.yml annotations
helm-docs:
	helm-docs --chart-search-root=helm --template-files=README.md.gotmpl --output-file=README.md

## helm-docs-check: Fail if helm/README.md is out of sync with helm/values.yml
helm-docs-check: helm-docs
	@git diff --exit-code -- helm/README.md \
	  || (echo "helm/README.md is out of sync with helm/values.yml — run 'make helm-docs' and commit the result." && exit 1)
