FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY v2/ .
RUN CGO_ENABLED=0 go build -o /hive ./cmd/hive

FROM node:22-alpine

ARG CLAUDE_CODE_VERSION=latest

RUN apk add --no-cache git ca-certificates bash curl tzdata \
 && npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
 && npm install -g @github/copilot \
 && npm cache clean --force

ENV HOME=/data/home
RUN mkdir -p /data/home

COPY --from=builder /hive /usr/local/bin/hive

COPY v2/proxy/ /opt/hive/proxy/
RUN cd /opt/hive/proxy && npm install --omit=dev

COPY dashboard/index.html /opt/hive/proxy/public/

EXPOSE 3001 3002
VOLUME ["/data", "/secrets"]

COPY v2/deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["entrypoint.sh"]
CMD ["--config", "/etc/hive/hive.yaml"]
