# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.2

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/limiter ./cmd/limiter
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sim ./cmd/sim

FROM alpine:3.22 AS runtime
WORKDIR /app
RUN addgroup -S golimiter && adduser -S -G golimiter golimiter
USER golimiter

FROM runtime AS limiter
COPY --from=build /out/limiter /app/limiter
COPY config/limiter.yaml /app/config/limiter.yaml
EXPOSE 50051
ENTRYPOINT ["/app/limiter"]

FROM runtime AS sim
COPY --from=build /out/sim /app/sim
ENTRYPOINT ["/app/sim"]
