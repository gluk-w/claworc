FROM node:22-alpine AS frontend

WORKDIR /build/frontend
COPY frontend/package.json frontend/package-lock.json frontend/.npmrc ./
RUN npm ci
COPY frontend/ .
RUN npx vite build

FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /build/frontend/dist /build/frontend/dist

RUN CGO_ENABLED=1 go build -ldflags "-X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o claworc .

FROM alpine:3.20

ARG USE_CHINA_MIRRORS=false
RUN if [ "$USE_CHINA_MIRRORS" = "true" ]; then \
      sed -i "s|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g" /etc/apk/repositories; \
    fi && \
    apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app

COPY --from=builder /build/claworc /app/claworc

EXPOSE 8000

ENTRYPOINT ["/app/claworc"]
