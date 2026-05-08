SHELL := /usr/bin/env bash

APP_NAME := sandboxd
CMD_PATH := ./cmd/server
BIN_DIR := ./build
BIN := $(BIN_DIR)/$(APP_NAME)

.PHONY: help fmt vet test build clean run install \
	start stop

help:
	@echo "Targets:"
	@echo "  make fmt           - gofmt all go files"
	@echo "  make vet           - go vet ./..."
	@echo "  make test          - go test ./..."
	@echo "  make build         - build server binary to ./build/sandboxd"
	@echo "  make run           - run server (loads .env via code)"
	@echo "  make clean         - remove build artifacts"
	@echo "  make install       - run scripts/install.sh (sudo may prompt)"
	@echo "  make start         - start server in background, log: /tmp/sandboxd.log"
	@echo "  make stop          - stop background server started by make start"

fmt:
	@go fmt ./...

vet:
	@go vet ./...

test:
	@go test ./...

build:
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN) $(CMD_PATH)

clean:
	@rm -rf $(BIN_DIR)

run:
	@go run $(CMD_PATH)/main.go

install:
	@./scripts/install.sh

start:
	@nohup go run $(CMD_PATH)/main.go >/tmp/sandboxd.log 2>&1 & echo $$! >/tmp/sandboxd.pid
	@echo "started pid=$$(cat /tmp/sandboxd.pid), log=/tmp/sandboxd.log"

stop:
	@if [[ -f /tmp/sandboxd.pid ]]; then \
		kill "$$(cat /tmp/sandboxd.pid)" && rm -f /tmp/sandboxd.pid && echo "stopped"; \
	else \
		echo "no pid file"; \
	fi
