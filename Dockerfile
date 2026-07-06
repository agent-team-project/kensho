# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN set -eux; \
    ldflags="-s -w -X github.com/agent-team-project/agent-team/internal/cli.Version=${VERSION}"; \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="${ldflags}" -o /out/agent-team ./cmd/agent-team; \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="${ldflags}" -o /out/agent-teamd ./cmd/agent-teamd

FROM alpine:3.20

RUN apk add --no-cache bash ca-certificates curl git github-cli nodejs npm openssh-client python3 \
    && npm install -g @openai/codex \
    && mkdir -p /root/.codex /root/.config/gh

COPY --from=build /out/agent-team /usr/local/bin/agent-team
COPY --from=build /out/agent-teamd /usr/local/bin/agent-teamd

WORKDIR /workspace

ENTRYPOINT ["agent-team"]
CMD ["--help"]
