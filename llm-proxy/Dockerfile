FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o llm-proxy .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app

COPY --from=builder /build/llm-proxy /app/llm-proxy

EXPOSE 8080

ENTRYPOINT ["/app/llm-proxy"]
