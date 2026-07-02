# =============================================================================
# Stage 1: Build
# =============================================================================
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Leverage Docker cache: copy go.mod first then download deps
COPY go.mod ./
RUN go mod download 2>/dev/null || true

# Copy the entire source tree
COPY . .

# Build a statically-linked binary
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/timetablex .

# =============================================================================
# Stage 2: Runtime
# =============================================================================
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

# Create a non-root user
RUN adduser -D -h /app appuser

WORKDIR /app

COPY --from=builder /out/timetablex .

# Web server port (default 8080, override with -p flag)
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/ || exit 1

USER appuser

# Default: start the web server on port 8080
ENTRYPOINT ["/app/timetablex"]
CMD ["-p", "8080"]
