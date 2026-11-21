# JellyIDE development tasks.
#
# Pinned tool versions (kept in sync with backend/.golangci.yml and
# .github/workflows/ci.yml):
GOLANGCI_LINT_VERSION := 2.15.0
RUFF_VERSION := 0.17.6

.PHONY: fmt lint test build check

## fmt: goimports + gofmt (backend), ruff format (python), prettier if present
fmt:
	cd backend && gofmt -w .
	@command -v goimports >/dev/null 2>&1 \
		&& cd backend && goimports -w -local yt-scraper . \
		|| echo "goimports not installed; ran gofmt only"
	ruff format backend
	@if test -x frontend/node_modules/.bin/prettier; then \
		cd frontend && ./node_modules/.bin/prettier --write .; \
	elif command -v prettier >/dev/null 2>&1; then \
		prettier --write frontend/src; \
	else \
		echo "prettier not present; skipping"; \
	fi

## lint: golangci-lint (backend), ruff check (python), frontend lint if the script exists
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
	@if grep -q '"lint"' frontend/package.json 2>/dev/null; then \
		cd frontend && npm run lint; \
	else \
		echo "frontend lint script not present; skipping"; \
	fi

## test: go test -race -cover (backend), frontend tests if they exist
test:
	cd backend && go test -race -cover ./...
	@if grep -q '"test"' frontend/package.json 2>/dev/null; then \
		cd frontend && npm test; \
	else \
		echo "frontend test script not present; skipping"; \
	fi

## build: backend and frontend builds
build:
	cd backend && go build ./...
	@test -d frontend/node_modules || cd frontend && npm ci
	cd frontend && npm run build

## check: lint, test and build
check: lint test build
