all: build

build:
	go build -o bin/erwtensoep ./cmd

test:
	go test ./...

vet:
	go vet ./...
