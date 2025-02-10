# numberlyinfra/vault-injector
FROM golang:1.23.6-alpine3.21 AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /vault-db-injector

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=build /vault-db-injector /vault-db-injector

USER 65534
EXPOSE 8443 8080

ENTRYPOINT ["/vault-db-injector"]