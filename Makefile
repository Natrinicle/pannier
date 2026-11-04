.PHONY: test run vet

test:
	go test ./...

run:
	go run ./cmd/pannierd

vet:
	go vet ./...
