.PHONY: build fmt check clean all eino-agent-build eino-agent-test eino-agent-eval eino-agent-adk-eval

# Go binaries to build
BINARIES := bash_tool chat edit_tool list_files read

# Build all binaries
build:
	@echo "Building binaries..."
	go build -o bash_tool bash_tool.go
	go build -o chat chat.go
	go build -o edit_tool edit_tool.go
	go build -o list_files list_files.go
	go build -o read read.go

# Format all Go files
fmt:
	@echo "Formatting Go files..."
	go fmt ./...

# Check (lint and vet) all Go files
check:
	@echo "Running go vet on individual files..."
	go vet bash_tool.go
	go vet chat.go
	go vet edit_tool.go
	go vet list_files.go
	go vet read.go
	@echo "Running go mod tidy..."
	go mod tidy

# Clean built binaries
clean:
	@echo "Cleaning binaries..."
	rm -f $(BINARIES)

# Build everything and run checks
all: fmt check build

# Eino agent module targets
eino-agent-build:
	cd eino-agent && go build ./...

eino-agent-test:
	cd eino-agent && go vet ./... && go test ./...

eino-agent-eval:
	cd eino-agent && go test ./internal/agent -run TestEvalScenarios -v

eino-agent-adk-eval:
	cd eino-agent && go test ./internal/agentadk -run TestADKEvalScenarios -v

# Default target
.DEFAULT_GOAL := all
