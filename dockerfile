# Stage 1: Build Node.js frontend
FROM node:25-alpine AS frontend-builder

WORKDIR /app/internal/ui

# Copy package files
COPY internal/ui/package*.json ./

# Install dependencies
RUN npm ci

# Copy source code and config
COPY internal/ui/src ./src
COPY internal/ui/index.html ./
COPY internal/ui/vite.config.js ./

# Build frontend to dist/
RUN npm run build

# Stage 2: Build Go backend with embedded frontend
FROM golang:1.25-alpine AS backend-builder

WORKDIR /app

# Copy go module files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy backend source
COPY . .

# Copy built frontend assets from stage 1 to internal/ui/dist
RUN mkdir -p /app/internal/ui/dist
COPY --from=frontend-builder /app/internal/ui/dist/* /app/internal/ui/dist/
COPY --from=frontend-builder /app/internal/ui/dist/index.html /app/internal/ui/dist/

# Build the Go binary
RUN go build -o main .

# Stage 3: Runtime image
FROM alpine:latest

RUN apk add --no-cache git openssh tzdata docker-cli docker-cli-compose curl bash

# Install Infisical CLI
RUN curl -1sLf 'https://dl.cloudsmith.io/public/infisical/infisical-cli/setup.alpine.sh' | bash \
    && apk add --no-cache infisical

ENV TZ=""
RUN ln -sf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

RUN mkdir /repos && mkdir -p /root/.ssh && chmod 700 /root/.ssh

COPY --from=backend-builder /app/main /usr/local/bin/main

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/main"]
