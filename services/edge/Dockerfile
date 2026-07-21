FROM golang:1.25.5-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
COPY internal/ ./internal/
COPY services/edge/go.mod services/edge/go.sum ./services/edge/
COPY services/edge/cmd/ ./services/edge/cmd/
COPY services/edge/internal/ ./services/edge/internal/

WORKDIR /app/services/edge
RUN go build -o /edge ./cmd/edge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /edge /edge
EXPOSE 8081
CMD ["/edge"]
