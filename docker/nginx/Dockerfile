# syntax=docker/dockerfile:1
#
# TLS-terminating reverse proxy. openssl is added so the entrypoint can
# mint a self-signed certificate on first start; the certificate itself
# lives on a host-bind-mounted volume (see docker-compose.yml / .docker/)
# so it survives rebuilds and clients only have to trust it once.
FROM nginx:1.27-alpine

RUN apk add --no-cache openssl

COPY docker/nginx/nginx.conf /etc/nginx/conf.d/default.conf
COPY docker/nginx/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 443

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["nginx", "-g", "daemon off;"]
