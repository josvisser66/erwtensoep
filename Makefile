all:
	go build -o bin/erwtensoep cmd/*.go

vet:
	go vet cmd josvisser.nl/erwtensoep/internal/fs
