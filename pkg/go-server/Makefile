.PHONY: test test-unit test-integration test-all coverage clean release-patch release-minor release-major build install


# Run unit tests only
test-unit:
	go test -v ./parser ./generator ./diff ./introspect

# Run integration tests (requires PostgreSQL)
test-integration:
	go test -v -tags=integration ./...

# Run all tests
test: test-unit

test-all: test-unit test-integration

# Run tests with coverage
coverage:
	go test -coverprofile=coverage.out ./parser ./generator ./diff ./introspect
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run specific package tests
test-parser:
	go test -v ./parser

test-generator:
	go test -v ./generator

test-diff:
	go test -v ./diff

test-introspect:
	go test -v ./introspect

# Clean test artifacts
clean:
	rm -f coverage.out coverage.html
	rm -f tools/db-migrator/parser/struct_parser_test_new.go

# Run linter
lint:
	golangci-lint run ./...

# Build the binary
build:
	go build -o bin/db-migrator .

# Install the binary
install:
	go install

# Release commands
release-patch:
	@echo "Creating patch release..."
	@current_version=$$(git tag -l "v*.*.*" | sort -V | tail -1 || echo "v0.0.0"); \
	new_version=$$(echo $$current_version | awk -F. '{$$3 = $$3 + 1; print $$1"."$$2"."$$3}'); \
	echo "Bumping from $$current_version to $$new_version"; \
	if git rev-parse $$new_version >/dev/null 2>&1; then \
		echo "Error: Tag $$new_version already exists"; \
		exit 1; \
	fi; \
	sed -i.bak 's/Version[[:space:]]*=[[:space:]]*"[^"]*"/Version   = "'$${new_version#v}'"/' cmd/version.go && rm cmd/version.go.bak; \
	git add cmd/version.go; \
	git commit -m "Bump version to $$new_version"; \
	git tag $$new_version; \
	git push origin $$new_version; \
	git push origin main; \
	echo "Released $$new_version"

release-minor:
	@echo "Creating minor release..."
	@current_version=$$(git tag -l "v*.*.*" | sort -V | tail -1 || echo "v0.0.0"); \
	new_version=$$(echo $$current_version | awk -F. '{$$2 = $$2 + 1; $$3 = 0; print $$1"."$$2"."$$3}'); \
	echo "Bumping from $$current_version to $$new_version"; \
	if git rev-parse $$new_version >/dev/null 2>&1; then \
		echo "Error: Tag $$new_version already exists"; \
		exit 1; \
	fi; \
	sed -i.bak 's/Version[[:space:]]*=[[:space:]]*"[^"]*"/Version   = "'$${new_version#v}'"/' cmd/version.go && rm cmd/version.go.bak; \
	git add cmd/version.go; \
	git commit -m "Bump version to $$new_version"; \
	git tag $$new_version; \
	git push origin $$new_version; \
	git push origin main; \
	echo "Released $$new_version"

release-major:
	@echo "Creating major release..."
	@current_version=$$(git tag -l "v*.*.*" | sort -V | tail -1 || echo "v0.0.0"); \
	new_version=$$(echo $$current_version | awk -F. '{$$1 = $$1 + 1; $$2 = 0; $$3 = 0; print $$1"."$$2"."$$3}' | sed 's/^v/v/'); \
	echo "Bumping from $$current_version to $$new_version"; \
	if git rev-parse $$new_version >/dev/null 2>&1; then \
		echo "Error: Tag $$new_version already exists"; \
		exit 1; \
	fi; \
	sed -i.bak 's/Version[[:space:]]*=[[:space:]]*"[^"]*"/Version   = "'$${new_version#v}'"/' cmd/version.go && rm cmd/version.go.bak; \
	git add cmd/version.go; \
	git commit -m "Bump version to $$new_version"; \
	git tag $$new_version; \
	git push origin $$new_version; \
	git push origin main; \
	echo "Released $$new_version"

.DEFAULT_GOAL := test