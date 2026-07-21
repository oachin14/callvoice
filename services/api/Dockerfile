FROM golang:1.25.5-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY cmd/ ./cmd/
RUN go build -o /api ./cmd/api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /api /api
EXPOSE 8080
CMD ["/api"]
