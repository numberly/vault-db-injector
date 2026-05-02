.DEFAULT_GOAL := build

VERSION ?= $(shell ./hack/get-version.sh)

GO_IMG ?= golang:alpine3.20@sha256:1b455a3f7786e5765dbeb4f7ab32a36cdc0c3f4ddd35406606df612dc6e3269b

.PHONY: fmt vet build unit-test integration-test

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

# BPF_LIBBPF_INCLUDE: path to libbpf headers (bpf_helpers.h etc.)
# Defaults to the kernel headers shipped on the build host.
# On hosts with libbpf-dev installed this can be overridden to /usr/include.
BPF_LIBBPF_INCLUDE ?= /usr/src/linux-headers-$(shell uname -r)/tools/bpf/resolve_btfids/libbpf/include

.PHONY: bpf-headers
## bpf-headers: Re-generate pkg/bpf/c/headers/vmlinux.h from the running kernel's BTF
bpf-headers:
	mkdir -p pkg/bpf/c/headers
	sudo bpftool btf dump file /sys/kernel/btf/vmlinux format c > pkg/bpf/c/headers/vmlinux.h

.PHONY: build-bpf
## build-bpf: Compile BPF program for amd64; arm64 is built by docker buildx in CI
build-bpf:
	clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -I pkg/bpf/c/headers -I $(BPF_LIBBPF_INCLUDE) \
		-c pkg/bpf/c/substitute.bpf.c -o pkg/bpf/substitute.amd64.bpf.o
	@echo "arm64 BPF object built by docker buildx CI; skipping locally"

.PHONY: integration-test-bpf
## integration-test-bpf: Run BPF integration tests (requires CAP_BPF + LSM enabled kernel)
integration-test-bpf:
	go test -tags=integration_bpf -count=1 ./pkg/bpf/...

.PHONY: verify-bpf-object
## verify-bpf-object: Check that the committed BPF object has the same ELF section structure as the C source.
## Uses structural section comparison (readelf) instead of byte-exact cmp to avoid clang version sensitivity.
verify-bpf-object: build-bpf
	@echo "Comparing BPF object structure..."
	@readelf -SW pkg/bpf/substitute.amd64.bpf.o | grep -E "lsm|maps|substitute_envp" > /tmp/bpf-committed-sections.txt
	@clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -I pkg/bpf/c/headers -I $(BPF_LIBBPF_INCLUDE) \
		-c pkg/bpf/c/substitute.bpf.c -o /tmp/bpf-fresh.o
	@readelf -SW /tmp/bpf-fresh.o | grep -E "lsm|maps|substitute_envp" > /tmp/bpf-fresh-sections.txt
	@diff /tmp/bpf-committed-sections.txt /tmp/bpf-fresh-sections.txt > /dev/null \
		|| { echo "ERROR: pkg/bpf/substitute.amd64.bpf.o is out of date with substitute.bpf.c. Run 'make build-bpf' and commit the result."; exit 1; }
	@echo "OK: BPF object structure matches source."
