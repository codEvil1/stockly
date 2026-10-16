.DEFAULT_GOAL := help
.PHONY: help run build test cover test-integration lint fmt vet tidy up down logs migrate-up migrate-down docker-build

DB_DSN       ?= root:root@tcp(127.0.0.1:3307)/stockly?parseTime=true&loc=UTC
MIGRATE_DB   ?= mysql://root:root@tcp(mysql:3306)/stockly?multiStatements=true

help: ## Lista os comandos disponíveis
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

run: ## Roda a API localmente (precisa do banco: make up)
	DB_DSN='$(DB_DSN)' go run ./cmd/api

build: ## Compila o binário em bin/stockly-api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/stockly-api ./cmd/api

test: ## Testes unitários com detector de race
	go test -race -count=1 ./...

cover: ## Testes com relatório de cobertura (coverage.html)
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

test-integration: ## Sobe o MySQL, aplica as migrations e roda os testes de integração
	docker compose up -d --wait mysql
	docker compose run --rm migrate
	DB_DSN='$(DB_DSN)' go test -race -count=1 -tags integration ./internal/orders/infra/mysql/...

lint: ## golangci-lint (https://golangci-lint.run/welcome/install/)
	golangci-lint run

fmt: ## Formata o código
	gofmt -w .

vet: ## go vet (com e sem a tag de integração)
	go vet ./...
	go vet -tags integration ./...

tidy: ## Sincroniza go.mod/go.sum
	go mod tidy

up: ## Sobe MySQL + migrations + API via docker compose
	docker compose up -d --build api
	@echo "Aguardando a API ficar pronta..."
	@for i in $$(seq 1 60); do curl -sf http://localhost:8080/readyz >/dev/null && break; sleep 1; done; \
	  curl -sf http://localhost:8080/readyz >/dev/null \
	  && echo "API: http://localhost:8080   Docs: http://localhost:8080/docs" \
	  || { echo "A API não respondeu em 60s. Veja: make logs"; exit 1; }

down: ## Derruba tudo e apaga o volume do banco
	docker compose down -v

logs: ## Acompanha os logs da API
	docker compose logs -f api

migrate-up: ## Aplica as migrations pendentes
	docker compose run --rm migrate

migrate-down: ## Desfaz a última migration
	docker compose run --rm migrate -path=/migrations -database="$(MIGRATE_DB)" down 1

docker-build: ## Builda a imagem da API
	docker build -t stockly-api:local .
