FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY v2/ .
RUN CGO_ENABLED=0 go build -o /hive ./cmd/hive

FROM alpine:3.21
RUN apk add --no-cache git ca-certificates
COPY --from=builder /hive /usr/local/bin/hive
EXPOSE 3001
VOLUME ["/data", "/secrets"]
ENTRYPOINT ["hive"]
CMD ["--config", "/etc/hive/hive.yaml"]
