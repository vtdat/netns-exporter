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

# Install required runtime dependencies:
# - ca-certificates: for HTTPS calls
# - iputils: provides ping command for connectivity monitoring
# - iproute2: provides ip command for namespace operations
RUN apk --no-cache add \
    ca-certificates \
    iputils \
    iproute2

# Create config and log directories
RUN mkdir -p /etc/netns-exporter /var/log/netns-exporter

# Copy binary from builder to standard bin path
COPY --from=builder /app/netns-exporter /usr/local/bin/netns-exporter

# Set working directory
WORKDIR /var/log/netns-exporter

# Expose Prometheus metrics port (default 9101)
EXPOSE 9101

# Run as non-root user for security (optional, may need host privileges for netns)
# Note: Commented out as netns operations typically require elevated privileges
# RUN addgroup -g 1000 netns && adduser -D -u 1000 -G netns netns
# USER netns

# Entrypoint allows arguments to be passed to the binary
ENTRYPOINT ["/usr/local/bin/netns-exporter"]

# CMD provides default arguments that can be overridden
CMD ["--config", "/etc/netns-exporter/config.yaml"]
