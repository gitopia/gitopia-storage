# Use a specific Go version for reproducibility
FROM golang:1.23-alpine AS builder
WORKDIR /src/app/

# Install build dependencies
RUN apk add --no-cache curl make git gcc libc-dev linux-headers file

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build statically linked binary
COPY . .
RUN LINK_STATICALLY=true make build

# Verify static linking
RUN file /src/app/build/gitopia-storaged | grep "statically linked"

# --- Final Image ---
FROM alpine:latest
WORKDIR /app

# Add dependencies needed at runtime
RUN apk add --no-cache git supervisor

# Add a non-root user for security
RUN addgroup -S gitopia && adduser -S gitopia -G gitopia
# Create directories and set permissions
RUN mkdir -p /var/repos /var/lfs-objects /var/attachments /app \
    && chown -R gitopia:gitopia /var/repos /var/lfs-objects /var/attachments /app

# Copy binaries from builder stage
COPY --from=builder /src/app/build/gitopia-storaged /usr/local/bin/
COPY --from=builder /src/app/build/gitopia-pre-receive /usr/local/bin/
COPY --from=builder /src/app/build/gitopia-post-receive /usr/local/bin/

# Copy default production config and supervisor config
COPY config_prod.toml /app/config_prod.toml
COPY scripts/entrypoint.sh /app/entrypoint.sh
COPY scripts/supervisord.conf /etc/supervisord.conf

RUN chmod +x /app/entrypoint.sh

ENV ENV="PRODUCTION"

# Switch to the non-root user
USER gitopia

EXPOSE 5000

ENTRYPOINT ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
