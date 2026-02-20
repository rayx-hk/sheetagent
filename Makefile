VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build precheck bench-200 bench-400 bench-912 bench-full docker-build docker-up docker-down download-dataset test lint clean

build:
	go build $(GO_LDFLAGS) -o bin/agent ./cmd/agent
	go build $(GO_LDFLAGS) -o bin/bench ./cmd/bench
	go build $(GO_LDFLAGS) -o bin/precheck ./cmd/precheck

precheck:
	go run ./cmd/precheck

bench-200: build
	./bin/bench -dataset 200

bench-400: build
	./bin/bench -dataset 400

bench-912: build
	./bin/bench -dataset 912

bench-full: build
	./bin/bench -dataset full

docker-build:
	docker build -t spreadsheet-agent-executor:latest -f docker/Dockerfile.executor docker/

docker-up:
	docker compose -f docker/docker-compose.yaml up -d

docker-down:
	docker compose -f docker/docker-compose.yaml down

download-dataset:
	bash scripts/download_dataset.sh ./data

test:
	go test ./... -v -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ 
