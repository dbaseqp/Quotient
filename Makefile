.PHONY: test test-unit test-integration test-coverage clean test-deps-start test-deps-stop 

test:
	go test -v -race -p 1 ./...

test-unit:
	go test -v -race -short ./engine/... ./www/... ./runner/...

test-integration: test-deps-start
	go test -v -race -p 1 ./tests/integration/... ./engine/... -timeout 10m
	@$(MAKE) test-deps-stop

test-deps-start:
	docker compose up redis db --wait -d

test-deps-stop:
	docker compose down

test-coverage:
	go test -race -p 1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean: test-deps-stop
	rm -f coverage.out coverage.html
