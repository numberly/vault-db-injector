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
## build-bpf: Compile BPF program for amd64 and arm64 (cross-compile via -target bpf + __TARGET_ARCH_*)
build-bpf:
	clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -I pkg/bpf/c/headers -I $(BPF_LIBBPF_INCLUDE) \
		-c pkg/bpf/c/substitute.bpf.c -o pkg/bpf/substitute.amd64.bpf.o
	clang -O2 -g -target bpf -D__TARGET_ARCH_arm64 -I pkg/bpf/c/headers -I $(BPF_LIBBPF_INCLUDE) \
		-c pkg/bpf/c/substitute.bpf.c -o pkg/bpf/substitute.arm64.bpf.o

.PHONY: integration-test-bpf
## integration-test-bpf: Run BPF integration tests (requires CAP_BPF + LSM enabled kernel)
integration-test-bpf:
	go test -tags=integration_bpf -count=1 ./pkg/bpf/...

.PHONY: verify-bpf-object
## verify-bpf-object: Check that the committed BPF object has the same ELF section structure as the C source.
## Uses structural section comparison (readelf) instead of byte-exact cmp to avoid clang version sensitivity.
verify-bpf-object: build-bpf
	@echo "Comparing BPF object structure (amd64)..."
	@readelf -SW pkg/bpf/substitute.amd64.bpf.o | grep -E "tracepoint|maps|sys_enter_execve|\.text|\.rodata|scan_envp|try_match" > /tmp/bpf-committed-sections.txt
	@clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -I pkg/bpf/c/headers -I $(BPF_LIBBPF_INCLUDE) \
		-c pkg/bpf/c/substitute.bpf.c -o /tmp/bpf-fresh-amd64.o
	@readelf -SW /tmp/bpf-fresh-amd64.o | grep -E "tracepoint|maps|sys_enter_execve|\.text|\.rodata|scan_envp|try_match" > /tmp/bpf-fresh-sections.txt
	@diff /tmp/bpf-committed-sections.txt /tmp/bpf-fresh-sections.txt > /dev/null \
		|| { echo "ERROR: pkg/bpf/substitute.amd64.bpf.o is out of date with substitute.bpf.c. Run 'make build-bpf' and commit the result."; exit 1; }
	@echo "Comparing BPF object structure (arm64)..."
	@readelf -SW pkg/bpf/substitute.arm64.bpf.o | grep -E "tracepoint|maps|sys_enter_execve|\.text|\.rodata|scan_envp|try_match" > /tmp/bpf-committed-arm64-sections.txt
	@clang -O2 -g -target bpf -D__TARGET_ARCH_arm64 -I pkg/bpf/c/headers -I $(BPF_LIBBPF_INCLUDE) \
		-c pkg/bpf/c/substitute.bpf.c -o /tmp/bpf-fresh-arm64.o
	@readelf -SW /tmp/bpf-fresh-arm64.o | grep -E "tracepoint|maps|sys_enter_execve|\.text|\.rodata|scan_envp|try_match" > /tmp/bpf-fresh-arm64-sections.txt
	@diff /tmp/bpf-committed-arm64-sections.txt /tmp/bpf-fresh-arm64-sections.txt > /dev/null \
		|| { echo "ERROR: pkg/bpf/substitute.arm64.bpf.o is out of date with substitute.bpf.c. Run 'make build-bpf' and commit the result."; exit 1; }
	@echo "OK: BPF object structure matches source (amd64 + arm64)."
