# numberlyinfra/vault-injector

# Stage 1: compile BPF C source for both supported architectures.
# Requires kernel headers and clang+libbpf to produce CO-RE-friendly objects.
FROM --platform=$BUILDPLATFORM ubuntu:24.04 AS bpf-builder
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      clang llvm libbpf-dev linux-libc-dev linux-headers-generic && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY pkg/bpf/c/substitute.bpf.c pkg/bpf/c/substitute.bpf.c
COPY pkg/bpf/c/headers/ pkg/bpf/c/headers/
RUN clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -I pkg/bpf/c/headers \
      -c pkg/bpf/c/substitute.bpf.c -o /tmp/substitute.amd64.bpf.o && \
    clang -O2 -g -target bpf -D__TARGET_ARCH_arm64 -I pkg/bpf/c/headers \
      -c pkg/bpf/c/substitute.bpf.c -o /tmp/substitute.arm64.bpf.o

FROM golang:1.24-alpine3.21 AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
# Overwrite committed stubs with freshly compiled BPF objects from bpf-builder.
COPY --from=bpf-builder /tmp/substitute.amd64.bpf.o pkg/bpf/substitute.amd64.bpf.o
COPY --from=bpf-builder /tmp/substitute.arm64.bpf.o pkg/bpf/substitute.arm64.bpf.o
RUN CGO_ENABLED=0 GOOS=linux go build -o /vault-db-injector

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=build /vault-db-injector /vault-db-injector

USER 65534
EXPOSE 8443 8080

ENTRYPOINT ["/vault-db-injector"]
