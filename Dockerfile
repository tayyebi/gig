# syntax=docker/dockerfile:1
#
# Runtime-only image: just the Go toolchain and CA certs. No source, no
# module cache, no compiled binary is baked in — all of that lives on
# host-bind-mounted volumes (see docker-compose.yml / .docker/) so it
# persists across container rebuilds and stays out of the image entirely.
FROM golang:1.26-alpine

RUN apk add --no-cache ca-certificates tzdata git

COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /app
EXPOSE 4099

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
