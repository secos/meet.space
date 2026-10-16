# syntax=docker/dockerfile:1

# --- CSS: Tailwind v4 standalone CLI (no Node.js). Runs on the build
# machine's own architecture; the output is the same everywhere. ---
FROM --platform=$BUILDPLATFORM alpine:3.24 AS css
ARG TAILWIND_VERSION=4.3.3
ARG BUILDARCH
RUN apk add --no-cache curl \
 && case "$BUILDARCH" in amd64) a=x64 ;; arm64) a=arm64 ;; *) echo "unsupported arch $BUILDARCH" && exit 1 ;; esac \
 && base=https://github.com/tailwindlabs/tailwindcss/releases/download/v${TAILWIND_VERSION} \
 && curl -fsSL -o /usr/local/bin/tailwindcss "$base/tailwindcss-linux-${a}-musl" \
 && curl -fsSL "$base/sha256sums.txt" | grep " ./tailwindcss-linux-${a}-musl$" | awk '{print $1 "  /usr/local/bin/tailwindcss"}' | sha256sum -c - \
 && chmod +x /usr/local/bin/tailwindcss
WORKDIR /src
COPY input.css ./
COPY templates/ ./templates/
COPY static/js/ ./static/js/
RUN tailwindcss -i input.css -o /out/output.css --minify

# --- Go build, cross-compiled for the target (arm64 on the t4g instance) ---
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine3.24 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /server ./cmd/server

# --- Runtime ---
FROM alpine:3.24
# tzdata is required for meeting time zones.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=build /server .
COPY templates/ ./templates/
COPY static/ ./static/
COPY --from=css /out/output.css ./static/css/output.css
USER app
EXPOSE 8080
CMD ["./server"]
