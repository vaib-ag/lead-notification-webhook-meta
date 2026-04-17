# --- Build Stage ---
FROM golang:1.24-alpine AS builder

# Set destination for COPY
WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
# -ldflags="-s -w" reduces binary size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o lead-webhook main.go

# --- Final Stage ---
FROM alpine:latest

# Certificates are required for HTTPS requests to Meta Graph API
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/lead-webhook .

# Copy environment example (user can mount their .env)
# COPY .env . (Only if you want .env baked in, generally use docker-compose)

# Expose port
EXPOSE 8654

# Run the binary
CMD ["./lead-webhook"]
