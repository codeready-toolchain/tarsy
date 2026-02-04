# It's necessary to set this because some environments don't link sh -> bash.
SHELL := /bin/bash

# Include all modular makefiles
include ./make/*.mk

.DEFAULT_GOAL := help
