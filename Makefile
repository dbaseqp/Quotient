.PHONY: test test-unit test-integration test-coverage clean test-deps-start test-deps-stop test-deps-wait

test:
	go test -race ./...

test-unit:
	go test -race -short ./engine/... ./www/... ./runner/...

test-integration: test-deps-start test-deps-wait
	POSTGRES_PASSWORD=postgres go test -race ./tests/integration/... -timeout 10m
	@$(MAKE) test-deps-stop

test-deps-start:
	@docker ps -q -f name=quotient-test-postgres | grep -q . || \
		docker run -d --name quotient-test-postgres \
			-e POSTGRES_PASSWORD=postgres \
			-e POSTGRES_DB=quotient_test \
			-p 5432:5432 \
			postgres:16-alpine
	@docker ps -q -f name=quotient-test-redis | grep -q . || \
		docker run -d --name quotient-test-redis \
			-p 6379:6379 \
			redis:7-alpine

test-deps-wait:
	@echo "Waiting for Postgres..."
	@until docker exec quotient-test-postgres pg_isready -U postgres > /dev/null 2>&1; do sleep 1; done
	@echo "Waiting for Redis..."
	@until docker exec quotient-test-redis redis-cli ping > /dev/null 2>&1; do sleep 1; done
	@echo "Services ready."

test-deps-stop:
	@docker stop quotient-test-postgres quotient-test-redis 2>/dev/null || true
	@docker rm quotient-test-postgres quotient-test-redis 2>/dev/null || true

test-coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean: test-deps-stop
	rm -f coverage.out coverage.html
