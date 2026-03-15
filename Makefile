.PHONY: test smoke test-pg sqlc

GO ?= $(shell if [ -x /usr/local/go/bin/go ]; then echo /usr/local/go/bin/go; else echo go; fi)
GOCACHE ?= $(CURDIR)/.cache/go-build

test:
	mkdir -p "$(GOCACHE)"
	GOCACHE="$(GOCACHE)" $(GO) test ./...

smoke:
	bash ./scripts/test/smoke.sh

test-pg:
	bash ./scripts/test/postgres.sh

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0 generate
