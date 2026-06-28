# syntax=docker/dockerfile:1
#
# Ouro Pass Issuer — single-image build (S0009).
# The issuer is one Go binary that serves the API, the embedded Admin SPA
# (/admin) and the background workers. This builds the SPA, bakes it into the Go
# binary via go:embed, and ships a minimal non-root runtime image.
#
# Build context is the repo root:  docker build -t ouropass/issuer .

# ---- stage 1: build the Admin SPA (web/) ----
# Pinned to the build host's arch ($BUILDPLATFORM): the SPA is just static JS, so
# build it once natively instead of under qemu for each target arch.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
RUN npm install -g pnpm@10
# install deps first (cached unless lockfile changes)
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build                                   # -> /web/dist

# ---- stage 2: build the Go issuer, embedding the SPA ----
# Toolchain runs natively ($BUILDPLATFORM); the binary is cross-compiled to the
# target arch via GOOS/GOARCH (pure Go, CGO off — zero-cost cross-compile).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
# download modules first (cached unless go.mod/go.sum change)
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
# place the freshly built SPA where //go:embed all:dist expects it
RUN rm -rf internal/httpapi/adminui/dist
COPY --from=web /web/dist ./internal/httpapi/adminui/dist
# static, stripped build (pure Go: modernc sqlite, CGO off)
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/issuer ./cmd/issuer

# ---- stage 3: minimal runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && addgroup -S ouro && adduser -S -G ouro ouro
COPY --from=build /out/issuer /usr/local/bin/issuer
USER ouro
EXPOSE 8080
# /healthz is served by the issuer router; gates compose depends_on.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/issuer"]
