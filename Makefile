.PHONY: test smoke test-pg sqlc

GO ?= $(shell if [ -x /usr/local/go/bin/go ]; then echo /usr/local/go/bin/go; else echo go; fi)
GOCACHE ?= $(CURDIR)/.cache/go-build

test:
	bash ./scripts/test/test.sh

smoke:
	bash ./scripts/test/test.sh

test-pg:
	bash ./scripts/test/test.sh

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 generate
