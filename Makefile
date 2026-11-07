.DEFAULT_GOAL := help
SHELL := /bin/bash

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
COMPOSE_DEV := .docker/compose.yaml
COMPOSE_INFRA := .docker/compose.infra.yaml
COMPOSE_TEST := .docker/compose.test.yaml
COMPOSE_E2E := .docker/compose.e2e-support.yaml
COMPOSE_PINNED_IMAGES := .docker/pinned-images.env
PROJECT_DEV := lateness-dev
PROJECT_INTEGRATION := lateness-integration
PROJECT_E2E := lateness-e2e

.PHONY: help render-env infra-up infra-down dev-up dev-down dev-restart serve-dev sweep-dev invite \
	check-config check-go vulncheck deploy-image backend-lint test-unit test validate test-integration test-e2e test-manual

help:
	@echo "Lateness — make targets"
	@echo "  render-env          write .docker/env/<project>.env (default $(PROJECT_DEV))"
	@echo "  infra-up            postgres + test SMTP inbox ($(PROJECT_DEV))"
	@echo "  infra-down          stop infra"
	@echo "  serve-dev           lateness serve on the host"
	@echo "  sweep-dev           lateness sweep on the host"
	@echo "  dev-up / dev-down   full stack, app processes in Docker"
	@echo "  dev-restart         recreate serve and sweep"
	@echo "  invite [ADMIN=1]    print a sign-up link for the dev stack"
	@echo "  check-config        render every env profile, docker compose config, bash -n on scripts"
	@echo "  check-go            gofmt, go mod tidy/verify, build, vet with integration and e2e tags"
	@echo "  vulncheck           govulncheck: known vulnerabilities in called code (needs network)"
	@echo "  backend-lint        golangci-lint (integration and e2e tags included)"
	@echo "  test-unit / test    go test -short -race (no Docker)"
	@echo "  validate            check-go + lint + unit"
	@echo "  test-integration    app scenarios against postgres (KEEP_INTEGRATION_STACK=1 keeps it)"
	@echo "  test-e2e            postgres, receiver, test SMTP, host serve+sweep"
	@echo "  deploy-image        build the production image (.deploy/Dockerfile) as lateness:local"
	@echo "  test-manual         everything in order: check-config, validate, vulncheck, deploy-image, integration, e2e"

render-env:
	@COMPOSE_PROJECT_NAME="$(or $(COMPOSE_PROJECT_NAME),$(PROJECT_DEV))" "$(ROOT)/.docker/scripts/render-env.sh"

infra-up:
	@$(MAKE) render-env COMPOSE_PROJECT_NAME=$(PROJECT_DEV)
	@set -euo pipefail; \
	set -a; . "$(ROOT)/.docker/env/$(PROJECT_DEV).env"; \
	if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env" ]; then . "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env"; fi; \
	set +a; \
	docker compose -f "$(ROOT)/$(COMPOSE_INFRA)" --env-file "$(ROOT)/$(COMPOSE_PINNED_IMAGES)" --env-file "$(ROOT)/.docker/env/$(PROJECT_DEV).env" -p $(PROJECT_DEV) up -d --wait --wait-timeout 120; \
	echo "Postgres: localhost:$$LATENESS_POSTGRES_PORT"; \
	echo "SMTP inbox: http://localhost:$$LATENESS_SMTP_UI_PORT (smtps localhost:$$LATENESS_SMTP_PORT)"

infra-down:
	@if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).env" ]; then \
	  docker compose -f "$(ROOT)/$(COMPOSE_INFRA)" --env-file "$(ROOT)/$(COMPOSE_PINNED_IMAGES)" --env-file "$(ROOT)/.docker/env/$(PROJECT_DEV).env" -p $(PROJECT_DEV) down; \
	fi

serve-dev: infra-up
	@set -euo pipefail; \
	set -a; . "$(ROOT)/.docker/env/$(PROJECT_DEV).env"; \
	if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env" ]; then . "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env"; fi; \
	set +a; \
	cd "$(ROOT)/backend"; \
	export LATENESS_DATABASE_URL="$$LATENESS_DATABASE_URL_HOST"; \
	export LATENESS_HTTP_ADDR=":$$LATENESS_HTTP_PORT"; \
	export LATENESS_SEED_SMTP_HOST="127.0.0.1"; \
	export LATENESS_SEED_SMTP_PORT="$$LATENESS_SMTP_PORT"; \
	go run ./cmd/lateness serve

sweep-dev:
	@set -euo pipefail; \
	set -a; . "$(ROOT)/.docker/env/$(PROJECT_DEV).env"; \
	if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env" ]; then . "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env"; fi; \
	set +a; \
	cd "$(ROOT)/backend"; \
	export LATENESS_DATABASE_URL="$$LATENESS_DATABASE_URL_HOST"; \
	go run ./cmd/lateness sweep

dev-up:
	@$(MAKE) render-env COMPOSE_PROJECT_NAME=$(PROJECT_DEV)
	@set -euo pipefail; \
	set -a; . "$(ROOT)/.docker/env/$(PROJECT_DEV).env"; \
	if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env" ]; then . "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env"; fi; \
	set +a; \
	docker compose -f "$(ROOT)/$(COMPOSE_DEV)" --env-file "$(ROOT)/$(COMPOSE_PINNED_IMAGES)" --env-file "$(ROOT)/.docker/env/$(PROJECT_DEV).env" -p $(PROJECT_DEV) up -d --build --wait --wait-timeout 300; \
	echo ""; \
	echo "Lateness dev is up."; \
	echo "  App:     $$LATENESS_PUBLIC_URL"; \
	echo "  SMTP inbox: http://localhost:$$LATENESS_SMTP_UI_PORT"; \
	echo "  Seed:    $$LATENESS_SEED_EMAIL / $$LATENESS_SEED_PASSWORD"

dev-down:
	@if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).env" ]; then \
	  docker compose -f "$(ROOT)/$(COMPOSE_DEV)" --env-file "$(ROOT)/$(COMPOSE_PINNED_IMAGES)" --env-file "$(ROOT)/.docker/env/$(PROJECT_DEV).env" -p $(PROJECT_DEV) down; \
	fi

dev-restart:
	@docker compose -f "$(ROOT)/$(COMPOSE_DEV)" --env-file "$(ROOT)/$(COMPOSE_PINNED_IMAGES)" --env-file "$(ROOT)/.docker/env/$(PROJECT_DEV).env" -p $(PROJECT_DEV) up -d --force-recreate --no-deps serve sweep

invite:
	@set -euo pipefail; \
	set -a; . "$(ROOT)/.docker/env/$(PROJECT_DEV).env"; \
	if [ -f "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env" ]; then . "$(ROOT)/.docker/env/$(PROJECT_DEV).local.env"; fi; \
	set +a; \
	cd "$(ROOT)/backend"; \
	LATENESS_DATABASE_URL="$$LATENESS_DATABASE_URL_HOST" go run ./cmd/lateness invite $(if $(ADMIN),-admin) -note "$(NOTE)"

# Every env profile renders against the template, every compose combination parses,
# and the shell scripts are syntactically valid.
check-config:
	@set -euo pipefail; \
	for project in $(PROJECT_DEV) $(PROJECT_INTEGRATION) $(PROJECT_E2E); do \
	  $(MAKE) --no-print-directory render-env COMPOSE_PROJECT_NAME=$$project; \
	done; \
	pinned="$(ROOT)/$(COMPOSE_PINNED_IMAGES)"; \
	envs="$(ROOT)/.docker/env"; \
	docker compose -f "$(ROOT)/$(COMPOSE_DEV)" --env-file "$$pinned" --env-file "$$envs/$(PROJECT_DEV).env" config --quiet; \
	docker compose -f "$(ROOT)/$(COMPOSE_TEST)" --env-file "$$pinned" --env-file "$$envs/$(PROJECT_INTEGRATION).env" config --quiet; \
	docker compose -f "$(ROOT)/$(COMPOSE_TEST)" -f "$(ROOT)/$(COMPOSE_E2E)" --env-file "$$pinned" --env-file "$$envs/$(PROJECT_E2E).env" config --quiet; \
	deploy_dir="$$(mktemp -d)"; \
	trap 'rm -rf "$$deploy_dir"' EXIT; \
	cp "$(ROOT)/.deploy/compose.yaml" "$(ROOT)/.deploy/config.env" "$$deploy_dir/"; \
	cp "$(ROOT)/.deploy/secrets.env.example" "$$deploy_dir/secrets.env"; \
	cat "$$deploy_dir/config.env" "$$deploy_dir/secrets.env" > "$$deploy_dir/.env"; \
	docker compose -f "$$deploy_dir/compose.yaml" config --quiet; \
	echo "compose files OK (dev, integration, e2e, deploy)"; \
	for script in "$(ROOT)"/scripts/*.sh "$(ROOT)"/scripts/lib/*.sh "$(ROOT)"/.docker/scripts/*.sh; do bash -n "$$script"; done; \
	echo "shell scripts OK"

# Formatting, module files, and compilation of code behind build tags (integration, e2e),
# which go build and go test -short never see.
check-go:
	@set -euo pipefail; \
	cd "$(ROOT)/backend"; \
	unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:" >&2; echo "$$unformatted" >&2; exit 1; fi; \
	go mod tidy -diff; \
	go mod verify; \
	go build ./...; \
	go vet -tags=integration,e2e ./...; \
	echo "go checks OK"

# Known vulnerabilities (vuln.go.dev) in dependencies and the standard library that the
# code actually reaches. The scanner version is pinned by the tool directive in go.mod.
vulncheck:
	@cd "$(ROOT)/backend" && go tool govulncheck -tags=integration,e2e ./...

# Production image from .deploy/Dockerfile, the same build CI pushes to GHCR.
deploy-image:
	@docker build -f "$(ROOT)/.deploy/Dockerfile" -t lateness:local \
	  --build-arg GIT_COMMIT="$$(git -C "$(ROOT)" rev-parse --short HEAD 2>/dev/null || echo dev)" "$(ROOT)"
	@echo "image lateness:local OK, version $$(docker run --rm lateness:local version)"

backend-lint:
	@cd "$(ROOT)/backend" && golangci-lint run --build-tags=integration,e2e ./...

test-unit:
	@cd "$(ROOT)/backend" && go test ./... -short -race -count=1

test: test-unit

validate: check-go backend-lint test-unit

# Full manual run, cheapest checks first; stops at the first failure.
test-manual:
	@echo "==> [1/6] config: env profiles, compose files (dev, test, deploy), shell scripts"
	@$(MAKE) --no-print-directory check-config
	@echo "==> [2/6] validate: gofmt, go.mod, build with tags, vet, golangci-lint, unit tests"
	@$(MAKE) --no-print-directory validate
	@echo "==> [3/6] vulncheck: known vulnerabilities in called code"
	@$(MAKE) --no-print-directory vulncheck
	@echo "==> [4/6] deploy image: production Dockerfile builds and the binary runs"
	@$(MAKE) --no-print-directory deploy-image
	@echo "==> [5/6] integration: app scenarios against Postgres"
	@$(MAKE) --no-print-directory test-integration
	@echo "==> [6/6] e2e: live serve and sweep, receiver, test SMTP"
	@$(MAKE) --no-print-directory test-e2e
	@echo ""
	@echo "ALL CHECKS PASSED"

test-integration:
	@$(MAKE) render-env COMPOSE_PROJECT_NAME=$(PROJECT_INTEGRATION)
	@set -euo pipefail; \
	root="$(ROOT)"; \
	env_file="$$root/.docker/env/$(PROJECT_INTEGRATION).env"; \
	local_env="$${env_file%.env}.local.env"; \
	compose() { \
	  if [ -f "$$local_env" ]; then \
	    docker compose -f "$$root/$(COMPOSE_TEST)" --env-file "$$root/$(COMPOSE_PINNED_IMAGES)" --env-file "$$env_file" --env-file "$$local_env" -p $(PROJECT_INTEGRATION) "$$@"; \
	  else \
	    docker compose -f "$$root/$(COMPOSE_TEST)" --env-file "$$root/$(COMPOSE_PINNED_IMAGES)" --env-file "$$env_file" -p $(PROJECT_INTEGRATION) "$$@"; \
	  fi; \
	}; \
	compose down -v >/dev/null 2>&1 || true; \
	set -a; . "$$env_file"; \
	if [ -f "$$local_env" ]; then . "$$local_env"; fi; \
	set +a; \
	cleanup() { \
	  ec=$$?; \
	  if [ "$${KEEP_INTEGRATION_STACK:-}" = "1" ]; then \
	    echo "KEEP_INTEGRATION_STACK=1: leaving $(PROJECT_INTEGRATION) running"; \
	    exit "$$ec"; \
	  fi; \
	  echo "tearing down $(PROJECT_INTEGRATION)..."; \
	  compose down -v || true; \
	  exit "$$ec"; \
	}; \
	trap cleanup EXIT; \
	compose up -d --wait --wait-timeout 120; \
	test_status=0; \
	(cd "$$root/backend" && LATENESS_INTEGRATION=1 LATENESS_DATABASE_URL="$$LATENESS_DATABASE_URL_HOST" go test -tags=integration ./internal/integration -count=1 -timeout 5m) || test_status=$$?; \
	if [ "$$test_status" -eq 0 ]; then echo "Integration tests PASSED"; else echo "Integration tests FAILED (exit $$test_status)" >&2; fi; \
	exit "$$test_status"

test-e2e:
	@$(MAKE) render-env COMPOSE_PROJECT_NAME=$(PROJECT_E2E)
	@set -euo pipefail; \
	root="$(ROOT)"; \
	env_file="$$root/.docker/env/$(PROJECT_E2E).env"; \
	local_env="$${env_file%.env}.local.env"; \
	compose() { \
	  if [ -f "$$local_env" ]; then \
	    docker compose -f "$$root/$(COMPOSE_TEST)" -f "$$root/$(COMPOSE_E2E)" --env-file "$$root/$(COMPOSE_PINNED_IMAGES)" --env-file "$$env_file" --env-file "$$local_env" -p $(PROJECT_E2E) "$$@"; \
	  else \
	    docker compose -f "$$root/$(COMPOSE_TEST)" -f "$$root/$(COMPOSE_E2E)" --env-file "$$root/$(COMPOSE_PINNED_IMAGES)" --env-file "$$env_file" -p $(PROJECT_E2E) "$$@"; \
	  fi; \
	}; \
	"$$root/scripts/e2e-host-services.sh" down >/dev/null 2>&1 || true; \
	compose down -v >/dev/null 2>&1 || true; \
	set -a; . "$$env_file"; \
	if [ -f "$$local_env" ]; then . "$$local_env"; fi; \
	set +a; \
	cleanup() { \
	  ec=$$?; \
	  if [ "$${KEEP_E2E_STACK:-}" = "1" ]; then \
	    echo "KEEP_E2E_STACK=1: leaving $(PROJECT_E2E) running"; \
	    exit "$$ec"; \
	  fi; \
	  echo "tearing down $(PROJECT_E2E)..."; \
	  "$$root/scripts/e2e-host-services.sh" down || true; \
	  compose down -v || true; \
	  exit "$$ec"; \
	}; \
	trap cleanup EXIT; \
	compose up -d --build --wait --wait-timeout 180; \
	"$$root/scripts/e2e-host-services.sh" up; \
	test_status=0; \
	(cd "$$root/backend" && LATENESS_E2E=1 \
	  LATENESS_BIN="$$root/.docker/e2e-host-run/bin/lateness" \
	  LATENESS_HTTP_URL="http://127.0.0.1:$$LATENESS_HTTP_PORT" \
	  LATENESS_RECEIVER_URL="http://127.0.0.1:$$LATENESS_RECEIVER_PORT" \
	  LATENESS_SMTP_API_URL="http://127.0.0.1:$$LATENESS_SMTP_API_PORT" \
	  go test -tags=e2e ./e2e -count=1 -timeout 4m) || test_status=$$?; \
	if [ "$$test_status" -eq 0 ]; then echo "E2E tests PASSED"; else echo "E2E tests FAILED (exit $$test_status)" >&2; fi; \
	exit "$$test_status"
