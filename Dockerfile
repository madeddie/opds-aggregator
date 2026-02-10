FROM golang:1.24-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o opds-aggregator .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/opds-aggregator /usr/local/bin/opds-aggregator

EXPOSE 8080

ENTRYPOINT ["opds-aggregator"]
