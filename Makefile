.PHONY: test run dev build docker-up docker-down

test:
	go test ./...
	node --test tests/*.test.cjs

run:
	go run .

dev:
	CONTENT_DIR=content go tool -modfile=dev.mod air --build.include_ext "go,html,css,js,yaml"

build:
	go build -o bin/manna .

docker-up:
	docker compose up --build

docker-down:
	docker compose down
