.PHONY: build build-admin build-all clean run test help

# Default target
all: build-all

# Build the mizu server
build:
	@echo "Building mizu server..."
	go build -o mizu ./cmd/mizu

# Build the mizu-admin CLI
build-admin:
	@echo "Building mizu-admin..."
	go build -o mizu-admin ./cmd/mizu-admin

# Build both executables
build-all: build build-admin
	@echo "Built mizu and mizu-admin successfully"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f mizu mizu-admin smtp-relay

# Run the server
run: build
	./mizu

# Run the server in local mode
run-local: build
	./mizu --local

# Run tests
test:
	go test -v ./...

# Run tests with coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# Build for production (with optimizations)
build-prod:
	@echo "Building mizu server (production)..."
	CGO_ENABLED=0 go build -ldflags="-s -w" -o mizu ./cmd/mizu
	@echo "Building mizu-admin (production)..."
	CGO_ENABLED=0 go build -ldflags="-s -w" -o mizu-admin ./cmd/mizu-admin

# Install dependencies
deps:
	go mod download
	go mod tidy

# Install binaries to $GOPATH/bin
install:
	go install ./cmd/mizu
	go install ./cmd/mizu-admin

# Help target
help:
	@echo "Available targets:"
	@echo "  build       - Build the mizu server"
	@echo "  build-admin - Build the mizu-admin CLI"
	@echo "  build-all   - Build both executables (default)"
	@echo "  clean       - Remove build artifacts"
	@echo "  run         - Build and run the server"
	@echo "  run-local   - Build and run the server in local mode"
	@echo "  test        - Run tests"
	@echo "  coverage    - Run tests with coverage report"
	@echo "  build-prod  - Build optimized production binaries"
	@echo "  install     - Install binaries to \$$GOPATH/bin"
	@echo "  deps        - Download and tidy dependencies"
	@echo "  help        - Show this help message"
