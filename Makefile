.PHONY: test run dev build docker-up docker-down

test:
	go test ./...
	node --test tests/*.test.cjs

run:
	go run .

# Runtime YAML reloads in-process; watching it would loop on the admin startup write check.
dev:
	MANNA_ADMIN_PASSWORD="$${MANNA_ADMIN_PASSWORD:-manna-local-development}" CONTENT_DIR=content go tool -modfile=dev.mod air --build.include_ext "go,html,css,js"

build:
	go build -o bin/manna .

docker-up:
	docker compose up --build

docker-down:
	docker compose down
