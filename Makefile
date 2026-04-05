.PHONY: build run clean lint test

lint:
	@echo "Running go vet..."
	@go vet ./...
	@echo "Lint passed."

build:
	@go build -ldflags "-X main.version=$$(git describe --tags --always)" -o bin/nevinho

run:
	@go run main.go

test:
	@go test ./... -count=1

clean:
	@rm -rf bin
