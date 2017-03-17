VERSION       := $(shell git describe --tags --always )
TARGET        := $(shell basename `git rev-parse --show-toplevel`)
TEST          ?= $(shell go list ./... | grep -v /vendor/)
REPOSITORY    := mattdeboer/etcdcd
DOCKER_IMAGE   = ${REPOSITORY}:${VERSION}

default: test build

test:
	go test -v -cover -run=$(RUN) $(TEST)

build: clean
	@go build -v -o bin/$(TARGET) ./pkg/cmd

release: clean
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build \
		-a -tags netgo \
		-a -installsuffix cgo \
    -ldflags "-s -X main.Version=$(VERSION) -X main.Name=$(TARGET)" \
		-o bin/$(TARGET) ./pkg/cmd

ca-certificates.crt:
	@-docker rm -f etcdcd_cacerts
	@docker run --name etcdcd_cacerts debian:latest bash -c 'apt-get update && apt-get install -y ca-certificates'
	@docker cp etcdcd_cacerts:/etc/ssl/certs/ca-certificates.crt .
	@docker rm -f etcdcd_cacerts

docker: release ca-certificates.crt
	@docker build -t ${DOCKER_IMAGE} .

clean:
	@rm -rf bin/