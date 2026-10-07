# It's necessary to set this because some environments don't link sh -> bash.
SHELL := /bin/bash

# Image builds read this so the compiler matches go.mod.
GO_VERSION := $(shell go mod edit -json | python3 -c 'import json, sys; print(json.load(sys.stdin)["Go"])')
export GO_VERSION

# Include all modular makefiles
include ./make/*.mk

.DEFAULT_GOAL := help
