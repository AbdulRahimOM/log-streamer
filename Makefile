.PHONY: dockerhub-push

DOCKERHUB_USERNAME ?= abdulrahimom
DOCKERHUB_PLATFORM ?= linux/amd64
IMAGE_TAG ?= $(shell git rev-parse HEAD)

# Run `docker login` first. IMAGE_TAG defaults to the current full Git SHA.
dockerhub-push:
	docker buildx build --platform $(DOCKERHUB_PLATFORM) --tag $(DOCKERHUB_USERNAME)/logstreamer:$(IMAGE_TAG) --tag $(DOCKERHUB_USERNAME)/logstreamer:latest --push .
