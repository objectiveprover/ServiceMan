PREFIX ?= $(HOME)/.local

build:
	go build -o serviceman .

install: build
	install -Dm755 serviceman $(PREFIX)/bin/serviceman
	@echo "installed $(PREFIX)/bin/serviceman — run 'serviceman logger install' to start log recording"

vet:
	go vet ./...

.PHONY: build install vet
