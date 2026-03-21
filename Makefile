.PHONY: build run clean lint

lint:
	@echo "Running go vet..."
	@go vet ./...
	@echo "Lint passed."

build:
	@echo "Building nevinho..."
	@go build -o bin/nevinho

run:
	@echo "Running nevinho..."
	@go run main.go

clean:
	@echo "Cleaning up..."
	@rm -rf bin
