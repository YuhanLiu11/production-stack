#!/bin/bash

# Set environment variable for adapter download path
export ADAPTER_DOWNLOAD_PATH="/mnt/models"

# Build the binary with debug information
go build -gcflags="all=-N -l" -o bin/manager main.go

# Start delve
dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./bin/manager 