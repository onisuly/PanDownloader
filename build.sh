#!/usr/bin/env bash
gox -ldflags="-s -w" -output build/pandownloader-{{.OS}}-{{.Arch}}