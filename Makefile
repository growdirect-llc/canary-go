.PHONY: migrate-up migrate-down migrate-test-up sqlc-gen test \
        build-identity build-all build-edge-windows lint

# Default DATABASE_URL — override on command line
DATABASE_URL ?= postgres://growdirect:growdirect_dev@localhost:5432/canary_go?sslmode=disable
TEST_DATABASE_URL ?= postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable

migrate-up:
	migrate -path=deploy/migrations \
	        -database="$(DATABASE_URL)" up

migrate-down:
	migrate -path=deploy/migrations \
	        -database="$(DATABASE_URL)" down 1

migrate-test-up:
	migrate -path=deploy/migrations \
	        -database="$(TEST_DATABASE_URL)" up

sqlc-gen:
	sqlc generate

test:
	DATABASE_URL="$(TEST_DATABASE_URL)" \
	VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
	SESSION_SECRET="test-session-secret-at-least-32-bytes!" \
	INTERNAL_SERVICE_SECRET=test-internal-secret \
	go test ./... -v -count=1

build-identity:
	go build -o bin/identity ./cmd/identity

build-all:
	@for svc in identity tsp gateway chirp alert fox owl analytics hawk bull \
	            asset item inventory receiving transfer pricing employee \
	            customer returns report; do \
	    echo "Building $$svc..."; \
	    go build -o bin/$$svc ./cmd/$$svc || exit 1; \
	done
	@echo "Building edge..."
	go build -o bin/edge ./cmd/edge

build-edge-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/edge.exe ./cmd/edge

lint:
	go vet ./...
