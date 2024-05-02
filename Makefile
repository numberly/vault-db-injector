.DEFAULT_GOAL := build

.PHONY:fmt vet build
fmt:
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	go build

build-docker: vet
	docker build -t registry.numberly.in/team-infrastructure/kube-vault-db-injector:2.0.1 .

push-docker: build-docker
	docker push registry.numberly.in/team-infrastructure/kube-vault-db-injector:2.0.1