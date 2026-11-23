# JellyIDE development tasks.
#
# Pinned tool versions (kept in sync with backend/.golangci.yml and
# .github/workflows/ci.yml):
GOLANGCI_LINT_VERSION := 2.15.0
RUFF_VERSION := 0.17.6

.PHONY: fmt lint test build check

## fmt: goimports + gofmt (backend), ruff format (python), prettier (frontend)
fmt:
	cd backend && gofmt -w .
	@command -v goimports >/dev/null 2>&1 \
		&& cd backend && goimports -w -local yt-scraper . \
		|| echo "goimports not installed; ran gofmt only"
	ruff format backend
	cd frontend && npm run format

## lint: golangci-lint (backend), ruff check (python), eslint + prettier check (frontend)
lint:
	@v=$$(golangci-lint --version 2>/dev/null | awk '{print $$4}'); \
	if [ -n "$$v" ] && [ "$$v" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (found $$v)"; exit 1; \
	fi
	cd backend && golangci-lint run ./...
	@v=$$(ruff --version 2>/dev/null | awk '{print $$2}'); \
	if [ -n "$$v" ] && [ "$$v" != "$(RUFF_VERSION)" ]; then \
		echo "ruff $(RUFF_VERSION) is required (found $$v)"; exit 1; \
	fi
	ruff check backend
	cd frontend && npm run lint && npm run format:check

## test: go test -race -cover (backend), frontend tests with coverage
test:
	cd backend && go test -race -cover ./...
	cd frontend && npm run test:ci

## build: backend and frontend builds
build:
	cd backend && go build ./...
	@test -d frontend/node_modules || cd frontend && npm ci
	cd frontend && npm run build

## check: lint, test, build and the auth smoke script
check: lint test build
	bash -n scripts/smoke-auth.sh
	./scripts/smoke-auth.sh
