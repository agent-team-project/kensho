# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG SOURCE_REVISION
RUN set -eux; \
    case "$SOURCE_REVISION" in ''|*[!0-9a-fA-F]*) echo "SOURCE_REVISION must be one full 40-character git revision" >&2; exit 1;; esac; \
    [ "${#SOURCE_REVISION}" -eq 40 ]; \
    ldflags="-s -w -X github.com/agent-team-project/agent-team/internal/cli.Version=${VERSION} -X github.com/agent-team-project/agent-team/internal/buildinfo.LinkedSourceIdentity=agent-team-source-v1:git:${SOURCE_REVISION}:end"; \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="${ldflags}" -o /out/agent-team ./cmd/agent-team; \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="${ldflags}" -o /out/agent-teamd ./cmd/agent-teamd

FROM alpine:3.20

ARG CODEX_NPM_VERSION=0.144.1

# Keep Codex pinned rather than following npm latest; the latest tag can move
# before the referenced tarball has propagated, breaking unchanged image builds.
RUN apk add --no-cache bash ca-certificates curl git github-cli nodejs npm openssh-client python3 \
    && npm install -g "@openai/codex@${CODEX_NPM_VERSION}" \
    && mkdir -p /root/.codex /root/.config/gh

COPY --from=build /out/agent-team /usr/local/bin/agent-team
COPY --from=build /out/agent-teamd /usr/local/bin/agent-teamd

WORKDIR /workspace

ENTRYPOINT ["agent-team"]
CMD ["--help"]
