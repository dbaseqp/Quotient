.PHONY: test test-unit test-integration test-coverage clean

test:
	go test -race ./...

test-unit:
	go test -race -short ./engine/... ./www/... ./runner/...

test-integration:
	go test -race ./tests/integration/... -timeout 10m

test-coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -f coverage.out coverage.html
