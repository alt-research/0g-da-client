# Use the official Golang image to create a build environment
FROM golang:1.22 AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# # Copy go mod and sum files
# COPY go.mod go.sum ./

# # Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
# RUN go mod download

# Copy the source code into the container
COPY . .

# Build the combined_server
RUN cd disperser && make build_combined

# Use a minimal base image
FROM debian:stable-slim

# Install ca-certificates
RUN apt-get update && apt-get install -y ca-certificates

# Set the Current Working Directory inside the container
WORKDIR /root/

# Copy the pre-built binary from the builder stage
COPY --from=builder /app/disperser/bin/combined /usr/local/bin/combined

# Expose port if necessary (example: 8080)
# EXPOSE 8080

# Command to run the binary
ENTRYPOINT ["/usr/local/bin/combined"]