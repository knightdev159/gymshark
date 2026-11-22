.PHONY: run test cover bench lint build docker docker-run

run:
	go run ./cmd/server

test:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

bench:
	go test -run=^$$ -bench=. -benchmem ./internal/packing

lint:
	gofmt -l . && go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

docker:
	docker build -t pack-calculator .

docker-run: docker
	docker run --rm -p 8080:8080 pack-calculator
