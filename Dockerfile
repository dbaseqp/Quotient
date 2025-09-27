# builder
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o quotient

# runner
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY config/certs/. /usr/local/share/ca-certificates/
RUN update-ca-certificates
COPY --from=builder /src/quotient /usr/local/bin/quotient
WORKDIR /app

CMD ["quotient"]
