.PHONY: build install test clean validate run-example run-server deps fmt lint

# Build the jigsaw CLI
build:
	@echo "Building jigsaw..."
	@mkdir -p bin
	@go build -o bin/jigsaw ./cmd/jigsaw
	@echo "✓ Build complete: bin/jigsaw"

# Install jigsaw globally
install:
	@echo "Installing jigsaw..."
	@go install ./cmd/jigsaw
	@echo "✓ Installed globally"

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod verify
	@echo "✓ Dependencies ready"

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Validate configuration
validate:
	@echo "Validating configuration..."
	@go run ./cmd/jigsaw validate --config ./configs

# Run simple example
run-example:
	@echo "Running simple example..."
	@go run ./examples/simple/main.go

# Run server example
run-server:
	@echo "Starting server..."
	@go run ./examples/server/main.go

# Start server with CLI
serve:
	@echo "Starting jigsaw server..."
	@go run ./cmd/jigsaw serve --config ./configs --port 8080 --reload --pretty

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@echo "✓ Code formatted"

# Lint code (requires golangci-lint)
lint:
	@echo "Linting code..."
	@golangci-lint run ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@go clean
	@echo "✓ Cleaned"

# List available flows
list-flows:
	@go run ./cmd/jigsaw list flows --config ./configs

# List available tasks
list-tasks:
	@go run ./cmd/jigsaw list tasks --config ./configs

# List available providers
list-providers:
	@go run ./cmd/jigsaw list providers --config ./configs

# List available endpoints
list-endpoints:
	@go run ./cmd/jigsaw list endpoints --config ./configs

# Test a flow
test-flow:
	@go run ./cmd/jigsaw test flow basic_search \
		--config ./configs \
		--sub 1 \
		--input '{"query":"golang","limit":10}'

# Help
help:
	@echo "Jigsaw Makefile Commands:"
	@echo ""
	@echo "  make build          - Build the jigsaw CLI"
	@echo "  make install        - Install jigsaw globally"
	@echo "  make deps           - Download dependencies"
	@echo "  make test           - Run tests"
	@echo "  make validate       - Validate configuration"
	@echo "  make run-example    - Run simple example"
	@echo "  make run-server     - Run server example"
	@echo "  make serve          - Start jigsaw server"
	@echo "  make fmt            - Format code"
	@echo "  make lint           - Lint code"
	@echo "  make clean          - Clean build artifacts"
	@echo "  make list-flows     - List all flows"
	@echo "  make list-tasks     - List all tasks"
	@echo "  make list-providers - List all providers"
	@echo "  make list-endpoints - List all endpoints"
	@echo "  make test-flow      - Test a flow"
	@echo "  make help           - Show this help"
