# Stage 1: Build netns exporter
# Use the alpine variant for a smaller build footprint
FROM golang:1.23-alpine AS builder

WORKDIR /app

# 1. Download dependencies first (Better Layer Caching)
# Docker will cache this layer effectively if go.mod/go.sum don't change.
COPY go.mod go.sum ./
RUN go mod download

# 2. Copy source code
COPY . .

# 3. Build the binary
# -ldflags="-s -w": Strips debug information (smaller binary)
# -o: Output filename
ENV CGO_ENABLED=0
ENV GOOS=linux
RUN go build -ldflags="-s -w" -o netns-exporter .


# Stage 2: Prepare final image
FROM alpine:latest

# Install CA certificates (useful if your app makes HTTPS calls)
RUN apk --no-cache add ca-certificates

# Create config directory
RUN mkdir -p /etc/netns-exporter

# Copy binary from builder to standard bin path
COPY --from=builder /app/netns-exporter /usr/local/bin/netns-exporter

# Entrypoint allows arguments to be passed to the binary
ENTRYPOINT ["/usr/local/bin/netns-exporter"]

# CMD provides default arguments that can be overridden
CMD ["--config", "/etc/netns-exporter/config.yaml"]
