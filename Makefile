SHELL := /usr/bin/env bash

BIN_DIR := ./build
SBXLET_BIN := $(BIN_DIR)/sbxlet
SBXORCH_BIN := $(BIN_DIR)/sbxorch
SBXCTL_BIN := $(BIN_DIR)/sbxctl
SWAG := $(or $(shell command -v swag 2>/dev/null),$(shell go env GOPATH)/bin/swag)

SBXLET_CMD := ./cmd/sbxlet
SBXORCH_CMD := ./cmd/sbxorch
SBXCTL_CMD := ./cmd/sbxctl

.PHONY: help fmt vet test test-cover build build-sbxlet build-sbxorch build-sbxctl clean swagger swagger-sbxlet swagger-sbxorch \
	run-sbxlet run-sbxorch run-sbxctl install \
	start-sbxlet stop-sbxlet start-sbxorch stop-sbxorch

help:
	@echo "Targets:"
	@echo "  make fmt                - gofmt all go files"
	@echo "  make vet                - go vet ./..."
	@echo "  make test               - go test ./..."
	@echo "  make test-cover         - go test with coverage profile (coverage.out)"
	@echo "  make build              - build all binaries (sbxlet, sbxorch, sbxctl)"
	@echo "  make build-sbxlet       - build sbxlet binary"
	@echo "  make build-sbxorch      - build sbxorch binary"
	@echo "  make build-sbxctl       - build sbxctl binary"
	@echo "  make swagger            - generate swagger docs for sbxlet + sbxorch"
	@echo "  make swagger-sbxlet     - generate swagger docs for sbxlet"
	@echo "  make swagger-sbxorch    - generate swagger docs for sbxorch"
	@echo "  make run-sbxlet         - run sbxlet"
	@echo "  make run-sbxorch        - run sbxorch"
	@echo "  make run-sbxctl         - run sbxctl (pass ARGS='...')"
	@echo "  make clean              - remove build artifacts"
	@echo "  make install            - run scripts/install.sh"
	@echo "  make start-sbxlet       - start sbxlet in background (/tmp/sbxlet.log)"
	@echo "  make stop-sbxlet        - stop background sbxlet"
	@echo "  make start-sbxorch      - start sbxorch in background (/tmp/sbxorch.log)"
	@echo "  make stop-sbxorch       - stop background sbxorch"

fmt:
	@go fmt ./...

vet:
	@go vet ./...

test:
	@go test ./...

test-cover:
	@go test -p=1 -v -coverprofile=coverage.out ./...

build: build-sbxlet build-sbxorch build-sbxctl

build-sbxlet:
	@mkdir -p $(BIN_DIR)
	@go build -o $(SBXLET_BIN) $(SBXLET_CMD)

build-sbxorch:
	@mkdir -p $(BIN_DIR)
	@go build -o $(SBXORCH_BIN) $(SBXORCH_CMD)

build-sbxctl:
	@mkdir -p $(BIN_DIR)
	@go build -o $(SBXCTL_BIN) $(SBXCTL_CMD)

swagger: swagger-sbxlet swagger-sbxorch

swagger-sbxlet:
	@$(SWAG) init -g main.go -d cmd/sbxlet,sbxlet/http,sbxlet/model,sbxlet/config -o sbxlet/docs --instanceName sbxlet --parseDependency --parseInternal

swagger-sbxorch:
	@$(SWAG) init -g main.go -d cmd/sbxorch,sbxorch/http,sbxorch/http/handlers,sbxorch/service,sbxorch/types,sbxorch/config -o sbxorch/docs --instanceName sbxorch --parseDependency --parseInternal

clean:
	@rm -rf $(BIN_DIR)

run-sbxlet:
	@go run $(SBXLET_CMD)

run-sbxorch:
	@go run $(SBXORCH_CMD)

run-sbxctl:
	@go run $(SBXCTL_CMD) $(ARGS)

install:
	@./scripts/install.sh

start-sbxlet:
	@nohup go run $(SBXLET_CMD) >/tmp/sbxlet.log 2>&1 & echo $$! >/tmp/sbxlet.pid
	@echo "started sbxlet pid=$$(cat /tmp/sbxlet.pid), log=/tmp/sbxlet.log"

stop-sbxlet:
	@if [[ -f /tmp/sbxlet.pid ]]; then \
		kill "$$(cat /tmp/sbxlet.pid)" && rm -f /tmp/sbxlet.pid && echo "stopped sbxlet"; \
	else \
		echo "no sbxlet pid file"; \
	fi

start-sbxorch:
	@nohup go run $(SBXORCH_CMD) >/tmp/sbxorch.log 2>&1 & echo $$! >/tmp/sbxorch.pid
	@echo "started sbxorch pid=$$(cat /tmp/sbxorch.pid), log=/tmp/sbxorch.log"

stop-sbxorch:
	@if [[ -f /tmp/sbxorch.pid ]]; then \
		kill "$$(cat /tmp/sbxorch.pid)" && rm -f /tmp/sbxorch.pid && echo "stopped sbxorch"; \
	else \
		echo "no sbxorch pid file"; \
	fi
