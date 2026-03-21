.PHONY: build run clean lint

lint:
	@echo "Running go vet..."
	@go vet ./...
	@echo "Lint passed."

build:
	@go build -o bin/nevinho

run:
	@go run main.go

clean:
	@rm -rf bin
