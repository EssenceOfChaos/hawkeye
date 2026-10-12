# Loads .env if present so `make run-api` etc. pick up local settings.
ifneq (,$(wildcard .env))
include .env
export
endif

COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: help up down db-up migrate migrate-down run-api run-worker web-install web-dev fmt vet test test-race lint build check

help:
	@grep -E '^[a-z-]+:' Makefile | cut -d: -f1 | sort

up:            ## Start the full stack (db, migrations, api, web) in Docker
	$(COMPOSE) up --build
down:
	$(COMPOSE) down
db-up:         ## Start only PostgreSQL in Docker
	$(COMPOSE) up -d --wait db

migrate:       ## Apply migrations to DATABASE_URL
	go run ./cmd/migrate up
migrate-down:  ## Revert the latest migration
	go run ./cmd/migrate down

run-api:
	go run ./cmd/api
run-worker:    ## Scheduler + check workers (+ metrics on :9090)
	go run ./cmd/worker
web-install:
	cd web && npm ci
web-dev:
	cd web && npm run dev

fmt:
	gofmt -w .
vet:
	go vet ./...
test:          ## Go tests; PostgreSQL tests run when TEST_DATABASE_URL is set
	go test -count=1 ./...
test-race:
	go test -race -count=1 ./...
lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	cd web && npm run lint
build:
	go build -o bin/ ./cmd/...
	cd web && npm run build

check: lint test-race   ## Everything CI runs for the backend, plus lint
	cd web && npm test && npm run build
