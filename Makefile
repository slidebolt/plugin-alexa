IMAGE_NAME ?= slidebolt-plugin-alexa
TAG ?= latest

.PHONY: build test stage docker-build-local docker-build-prod clean

build:
	@echo "Building Plugin Alexa binary..."
	@mkdir -p bin
	go build -o bin/plugin-alexa ./cmd/main.go

test:
	@echo "Running tests..."
	go test ./...

# Pre-GitHub Bridge: Stage siblings so Docker can see them
stage:
	@echo "Staging sibling modules for Docker build..."
	@mkdir -p .stage
	@for mod in plugin-sdk plugin-framework; do \
		echo "Staging $$mod..."; \
		rm -rf .stage/$$mod; \
		cp -r ../$$mod .stage/$$mod; \
		rm -rf .stage/$$mod/.git; \
	done

docker-build-local: stage
	@echo "Building LOCAL Docker image $(IMAGE_NAME):$(TAG)..."
	docker build --build-arg BUILD_MODE=local -t $(IMAGE_NAME):$(TAG) .
	@echo "Cleaning up stage..."
	@rm -rf .stage

docker-build-prod:
	@echo "Building PROD Docker image $(IMAGE_NAME):$(TAG)..."
	docker build --build-arg BUILD_MODE=prod -t $(IMAGE_NAME):$(TAG) .

clean:
	@rm -rf bin/ .stage/ plugin-alexa
