FROM node:22-alpine AS frontend

WORKDIR /build/frontend
COPY frontend/package.json frontend/package-lock.json ./
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

RUN CGO_ENABLED=1 go build -o claworc .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app

COPY --from=builder /build/claworc /app/claworc

EXPOSE 8000

ENTRYPOINT ["/app/claworc"]
