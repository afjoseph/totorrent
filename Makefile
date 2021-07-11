SHELL := /bin/bash
BINARY_NAME ?= totorrent

#: Install stuff
build:
	go build -v -o $(BINARY_NAME) .

#: Test stuff
run:
	go run -v .
