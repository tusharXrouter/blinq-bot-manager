# Build stage
FROM golang:1.24-alpine AS builder

# Install git (needed for go mod download)
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the three binaries this repo ships
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bet-bot-manager ./cmd/bet-bot-manager && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sweep-manager ./cmd/sweep-manager && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o test-bet-bot-manager ./cmd/test-bet-bot-manager

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binaries and default config from builder stage
COPY --from=builder /app/bet-bot-manager .
COPY --from=builder /app/sweep-manager .
COPY --from=builder /app/test-bet-bot-manager .
COPY --from=builder /app/config.yaml .

# Create data directory for wallet storage
RUN mkdir -p /root/data

# Default: run the orchestrator non-interactively (config.yaml manager.enabled_bots picks the bots)
CMD ["./bet-bot-manager", "--non-interactive"]
