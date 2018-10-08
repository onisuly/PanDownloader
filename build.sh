#!/usr/bin/env bash

# Install gox: go get github.com/mitchellh/gox
if [ -d "build" ]; then
    rm -rf build
fi
gox -ldflags="-s -w" -output build/pandownloader-{{.OS}}-{{.Arch}}
