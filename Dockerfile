# Stage 1: Build the Go binary
FROM golang:1.23-alpine AS builder

# Set the working directory
WORKDIR /app

# Copy the Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Build the Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -o ./dist/libp2p-proxy ./cmd/libp2p-proxy

# Stage 2: Create the clean and slim base image
FROM alpine:3.18

# Install any required certificates or dependencies
RUN apk --no-cache add ca-certificates

# Copy the compiled binary from the builder stage
COPY --from=builder /app/dist/libp2p-proxy /usr/local/bin/libp2p-proxy
RUN chmod 777 /usr/local/bin/libp2p-proxy

EXPOSE 4001
EXPOSE 1080

# Set the binary as the entry point
ENTRYPOINT ["/usr/local/bin/libp2p-proxy"]
