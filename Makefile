.PHONY: all build dev clean frontend go lint lint-go lint-js test test-go test-js

all: build

build: frontend go

frontend: node_modules
	npx vite build

go: frontend
	go build -o tspages ./cmd/tspages

dev: node_modules
	npx vite build --watch

lint: lint-go lint-js

lint-go:
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(shell go env GOPATH)/bin/golangci-lint run ./...

lint-js: node_modules
	npm run lint

test: test-go test-js

test-go:
	go test -race ./...

test-js:
	@echo "No frontend tests configured."

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf internal/admin/assets/dist node_modules tspages
