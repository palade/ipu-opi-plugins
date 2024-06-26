# Copyright (c) 2023 Intel Corporation

.PHONY: all fmt check-fmt vet build ipuplugin test update-mod ipuplugin-amd64 ipuplugin-arm64
APP_NAME = ipu-p4-sdk
VERSION ?= 0.0.0
IMAGE_NAME = intel-$(APP_NAME)
ifneq (, $(IMAGE_REGISTRY))
IMAGE_TAG_BASE = $(IMAGE_REGISTRY)/$(IMAGE_NAME)
else
IMAGE_TAG_BASE = $(IMAGE_NAME)
endif
IMAGE_TAG_LATEST?=$(IMAGE_TAG_BASE):latest
IMAGE_TAG_VERSION=$(IMAGE_TAG_BASE):$(VERSION)
IMGTOOL ?= docker

DOCKERFILE?=$(CURDIR)/images/Dockerfile
DOCKERARGS=
ifdef HTTP_PROXY
	DOCKERARGS += --build-arg http_proxy=$(HTTP_PROXY)
endif
ifdef HTTPS_PROXY
	DOCKERARGS += --build-arg https_proxy=$(HTTPS_PROXY)
endif

all: image

image:
	# Copy p4 files from parent dir first
	cp -r ../e2e/artefacts/p4-rh_mvp $(CURDIR)/p4
	# $(IMGTOOL) build -t $(IMAGE_TAG_VERSION) -f $(DOCKERFILE)  $(CURDIR) $(DOCKERARGS)
	$(IMGTOOL) build -t $(IMAGE_TAG_VERSION) -t $(IMAGE_TAG_LATEST) -f $(DOCKERFILE)  $(CURDIR) $(DOCKERARGS)
	rm -rf p4

PLATFORMS ?= linux/arm64,linux/amd64
# https://stackoverflow.com/questions/73210141/running-buildkit-using-docker-buildx-behind-a-proxy
# 	docker buildx create --use --driver-opt env.http_proxy=$(HTTP_PROXY) --driver-opt env.https_proxy=$(HTTPS_PROXY) --driver-opt env.no_proxy=$(NO_PROXY)
imagex: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' $(DOCKERFILE) > $(DOCKERFILE).cross
	- $(IMGTOOL) buildx create --name image-builder --use --buildkitd-flags '--allow-insecure-entitlement security.insecure' --driver-opt env.http_proxy=$(HTTP_PROXY) --driver-opt env.https_proxy=$(HTTPS_PROXY) --driver-opt '"env.no_proxy='$(NO_PROXY)'"'
	$(IMGTOOL) buildx use image-builder
	- $(IMGTOOL) buildx build --allow security.insecure --push --platform=$(PLATFORMS) --tag ${IMAGE_TAG_VERSION} -f $(DOCKERFILE).cross  $(CURDIR) $(DOCKERARGS)
	- $(IMGTOOL) buildx rm image-builder
	rm $(DOCKERFILE).cross
