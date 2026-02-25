.PHONY: all build dev clean frontend go

all: build

build: frontend go

frontend: node_modules
	npx vite build

go: frontend
	go build -o tspages ./cmd/tspages

dev: node_modules
	npx vite build --watch

node_modules: package.json
	npm install
	@touch node_modules

clean:
	rm -rf internal/admin/assets/dist node_modules tspages
