# --- Build Stage ---
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src

ARG BUILD_MODE=prod

COPY . .

RUN if [ "$BUILD_MODE" = "local" ]; then \
        echo "Building in LOCAL mode using staged siblings..." && \
        rm -f go.work && \
        go work init . ./.stage/plugin-sdk ./.stage/plugin-framework; \
    else \
        echo "Building in PROD mode using remote modules..."; \
    fi

RUN go build -o /app/plugin-alexa ./cmd/main.go

# --- Run Stage ---
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/plugin-alexa /app/plugin-alexa

CMD ["/app/plugin-alexa"]
