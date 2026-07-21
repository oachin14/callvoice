FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY cmd/ ./cmd/
RUN go build -o /edge ./cmd/edge

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /edge /edge
EXPOSE 8081
CMD ["/edge"]
