#!/bin/sh
# Mints a self-signed certificate into the host-persisted cert dir on first
# start, then hands off to nginx. Regenerating is a matter of deleting the
# files: anything already present is left untouched.
set -e

CERT_DIR="${CERT_DIR:-/etc/nginx/certs}"
CERT="$CERT_DIR/server.crt"
KEY="$CERT_DIR/server.key"
# Names the certificate is valid for. Add the server's public hostname or IP
# here (comma-separated) so clients do not also hit a name mismatch on top of
# the expected self-signed warning.
CERT_HOSTS="${CERT_HOSTS:-localhost}"
CERT_DAYS="${CERT_DAYS:-3650}"

mkdir -p "$CERT_DIR"

if [ ! -f "$CERT" ] || [ ! -f "$KEY" ]; then
	echo "nginx: generating self-signed certificate for: $CERT_HOSTS"

	# Build the SAN list, sorting each entry into IP: or DNS: as openssl
	# requires. The CN is the first name; SANs are what clients actually check.
	san=""
	primary=""
	for host in $(echo "$CERT_HOSTS" | tr ',' ' '); do
		[ -n "$host" ] || continue
		[ -n "$primary" ] || primary="$host"
		case "$host" in
		*[!0-9.]*) san="$san,DNS:$host" ;;
		*) san="$san,IP:$host" ;;
		esac
	done
	primary="${primary:-localhost}"
	san="${san#,}"

	openssl req -x509 -newkey rsa:2048 -nodes \
		-keyout "$KEY" -out "$CERT" \
		-days "$CERT_DAYS" -sha256 \
		-subj "/CN=$primary" \
		-addext "subjectAltName=$san" \
		-addext "basicConstraints=critical,CA:FALSE" \
		-addext "keyUsage=critical,digitalSignature,keyEncipherment" \
		-addext "extendedKeyUsage=serverAuth"

	chmod 600 "$KEY"
	chmod 644 "$CERT"
else
	echo "nginx: reusing existing certificate at $CERT"
fi

exec "$@"
