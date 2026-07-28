.PHONY: all assets build run test compat clean

all: build

node_modules: package.json
	npm install
	@touch node_modules

assets: node_modules
	node web/build.mjs

build: assets
	go build -o bin/amythest ./cmd/amythest

run: assets
	go run ./cmd/amythest -vault $(HOME)/notes

test:
	go test ./...

compat: build
	bash scripts/compat.sh

clean:
	rm -rf bin web/dist
