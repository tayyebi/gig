# syntax=docker/dockerfile:1

# --- Build stage ---
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gig .

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 gig \
    && adduser -S -D -H -u 10001 -G gig gig

WORKDIR /app
COPY --from=build /out/gig /app/gig

USER gig
EXPOSE 8080

ENTRYPOINT ["/app/gig"]
