.DEFAULT_GOAL := help
SHELL := /bin/bash

POSTGRES_BASE_URL ?= postgres://korp:korp@localhost:5433
RABBITMQ_URL ?= amqp://korp:korp@localhost:5672/
SERVICE_TOKEN ?= local-development-token

.PHONY: help
help: ## Show the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: infra-up
infra-up: ## Start PostgreSQL and RabbitMQ
	docker compose up -d

.PHONY: infra-down
infra-down: ## Stop the infrastructure (keeps data)
	docker compose down

.PHONY: infra-reset
infra-reset: ## Stop the infrastructure and delete all data
	docker compose down -v

.PHONY: run-stock
run-stock: ## Run the stock service
	cd backend && HTTP_ADDR=:8081 \
		DATABASE_URL=$(POSTGRES_BASE_URL)/stock?sslmode=disable \
		RABBITMQ_URL=$(RABBITMQ_URL) \
		SERVICE_TOKEN=$(SERVICE_TOKEN) \
		IDENTITY_SERVICE_URL=http://localhost:8083 \
		go run ./cmd/stock-service

.PHONY: run-identity
run-identity: ## Run the identity service
	cd backend && HTTP_ADDR=:8083 \
		DATABASE_URL=$(POSTGRES_BASE_URL)/identity?sslmode=disable \
		SERVICE_TOKEN=$(SERVICE_TOKEN) \
		go run ./cmd/identity-service

.PHONY: run-billing
run-billing: ## Run the billing service
	cd backend && HTTP_ADDR=:8082 \
		DATABASE_URL=$(POSTGRES_BASE_URL)/billing?sslmode=disable \
		RABBITMQ_URL=$(RABBITMQ_URL) \
		SERVICE_TOKEN=$(SERVICE_TOKEN) \
		STOCK_SERVICE_URL=http://localhost:8081 \
		IDENTITY_SERVICE_URL=http://localhost:8083 \
		go run ./cmd/billing-service

.PHONY: test-backend
test-backend: ## Run the Go unit tests
	cd backend && go test ./...

.PHONY: test-backend-integration
test-backend-integration: ## Run the Go tests including the database integration tests, under the race detector
	cd backend && \
		TEST_DATABASE_URL=$(POSTGRES_BASE_URL)/stock_test?sslmode=disable \
		STOCK_TEST_DATABASE_URL=$(POSTGRES_BASE_URL)/stock_test?sslmode=disable \
		BILLING_TEST_DATABASE_URL=$(POSTGRES_BASE_URL)/billing_test?sslmode=disable \
		IDENTITY_TEST_DATABASE_URL=$(POSTGRES_BASE_URL)/identity_test?sslmode=disable \
		MESSAGING_TEST_DATABASE_URL=$(POSTGRES_BASE_URL)/messaging_test?sslmode=disable \
		RABBITMQ_TEST_URL=$(RABBITMQ_URL) \
		go test -race ./... -count=1

.PHONY: test-frontend
test-frontend: ## Run the Angular unit tests
	cd frontend && npm test

.PHONY: check-backend
check-backend: ## Format check, vet and test the Go code
	cd backend && test -z "$$(gofmt -l .)" && go vet ./... && go test ./...

.PHONY: check
check: check-backend test-frontend ## Run every automated check
