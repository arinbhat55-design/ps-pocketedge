MODULE   := github.com/ankitapaul1586-cmd/pspocketedge
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X '$(MODULE)/internal/shared/version.Version=$(VERSION)' \
            -X '$(MODULE)/internal/shared/version.Commit=$(COMMIT)' \
            -X '$(MODULE)/internal/shared/version.BuildDate=$(DATE)'

.PHONY: build build-agent build-controlplane build-cli test vet proto migrate dev release clean package-macos-setup

build: build-agent build-controlplane build-cli

build-cli:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/pse ./cmd/pse

build-agent:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/pe-agent ./cmd/agent

build-controlplane:
	go build -ldflags "$(LDFLAGS)" -o bin/pe-controlplane ./cmd/controlplane

test:
	go test ./...

vet:
	go vet ./...

proto:
	protoc -I proto -I $$(brew --prefix protobuf)/include \
		--go_out=. --go_opt=module=$(MODULE) \
		--go-grpc_out=. --go-grpc_opt=module=$(MODULE) \
		proto/agent/v1/agent.proto

migrate:
	migrate -path migrations -database "$${DATABASE_URL}" up

dev:
	docker compose -f deploy/docker-compose.dev.yml up

release:
	goreleaser release --snapshot --clean

package-macos-setup:
	bash scripts/package-macos-setup.sh

clean:
	rm -rf bin dist
