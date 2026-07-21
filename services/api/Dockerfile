FROM golang:1.25.5-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
COPY internal/ ./internal/
COPY services/api/go.mod services/api/go.sum ./services/api/
COPY services/api/cmd/ ./services/api/cmd/
COPY services/api/internal/ ./services/api/internal/
COPY services/api/migrations/ ./services/api/migrations/

WORKDIR /app/services/api
RUN go build -o /api ./cmd/api && go build -o /seed ./cmd/seed

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /api /api
COPY --from=builder /seed /seed
EXPOSE 8080
CMD ["/api"]
