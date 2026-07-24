BIN=bin/gomcp
MAIN=cmd/gomcp/main.go

.PHONY: all
all: build

.PHONY: tidy
tidy:
	@echo "Tidying up..."
	go fmt ./...
	go mod tidy

.PHONY: build
build: tidy
	@echo "Building..."
	go build -o $(BIN) $(MAIN)

.PHONY: run
run: build
	@echo "Running the server..."
	./$(BIN)

.PHONY: vet
vet:
	@echo "Vetting..."
	go vet ./...

.PHONY: test
test: vet
	@echo "Running tests..."
	go test -v ./...

.PHONY: test-race
test-race: vet
	@echo "Running tests with race detector..."
	go test -race ./...

.PHONY: test-short
test-short: vet
	@echo "Running tests (short, skips self-hosting smoke test)..."
	go test -short ./...

.PHONY: bench
bench:
	@echo "Running benchmarks..."
	go test -bench . -benchtime 3x ./internal/engine

.PHONY: inspect
inspect: build
	@npx @modelcontextprotocol/inspector ./$(BIN)

.PHONY: tags
tags:
	@echo "Generating tags..."
	find . -name "*.go" -print | etags -
