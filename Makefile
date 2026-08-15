GO ?= go

.PHONY: build run test lint vet fmt tidy up down logs psql migrate-fix favicon ci vulncheck

build:
	$(GO) build -o bin/gig .

run:
	$(GO) run . 

test:
	$(GO) test ./...

test-integration: up
	TEST_DATABASE_URL="postgres://gig:gig-dev-password@localhost:5432/gig?sslmode=disable" $(GO) test ./store/... -count=1

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .
	$(GO) mod tidy

lint: vet fmt-check

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

up:
	docker compose up -d --build

logs:
	docker compose logs -f

down:
	docker compose down

psql:
	docker compose exec db psql -U gig -d gig

favicon:
	python3 scripts/make-favicon.py static/favicon.ico

vulncheck:
	@command -v govulncheck >/dev/null 2>&1 || $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

ci: fmt-check vet build test
