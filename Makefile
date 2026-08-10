.PHONY: all assets build build-amy install-amy run test compat clean

all: build build-amy

node_modules: package.json
	npm install
	@touch node_modules

assets: node_modules
	node web/build.mjs

build: assets
	go build -o bin/amythest ./cmd/amythest

# amy is the terminal client; pure Go, no web assets needed.
build-amy:
	go build -o bin/amy ./cmd/amy

install-amy: build-amy
	install -m 0755 bin/amy $(HOME)/bin/amy

run: assets
	go run ./cmd/amythest -vault $(HOME)/notes

test:
	go test ./...

compat: build
	bash scripts/compat.sh

clean:
	rm -rf bin web/dist
