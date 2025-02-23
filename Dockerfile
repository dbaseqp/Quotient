# builder
FROM golang:1.24-alpine AS builder
RUN apk add git ca-certificates --update

WORKDIR /src
COPY . ./
RUN go mod download

RUN go build

# runner
FROM alpine:3.21
RUN apk add fortune
RUN apk add ca-certificates
COPY config/certs/. /usr/local/share/ca-certificates/
RUN update-ca-certificates
COPY --from=builder /src/quotient /usr/local/bin/quotient
WORKDIR /app

CMD ["quotient"]
