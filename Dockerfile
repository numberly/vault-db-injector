# registry.numberly.in/team-infrastructure/kube-vault-db-injector:1.0.0
FROM golang:1.21.8-alpine3.19 AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /kube-vault-db-injector

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=build /kube-vault-db-injector /kube-vault-db-injector

USER 65534
EXPOSE 8443 8080

ENTRYPOINT ["/kube-vault-db-injector"]