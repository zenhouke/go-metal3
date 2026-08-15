GO ?= go

.PHONY: all fmt tidy test vet verify

all: verify

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

verify: fmt tidy test vet
