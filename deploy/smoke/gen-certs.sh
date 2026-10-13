#!/usr/bin/env bash
# Creates a throwaway private CA and a server certificate for the local test mail server ("mailpit").
# Output goes to deploy/smoke/certs/ (gitignored). Test use only.
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p certs && cd certs
if [ -f ca.pem ] && [ -f server.pem ]; then exit 0; fi
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=HawkEye local test CA" -keyout ca.key -out ca.pem 2>/dev/null
openssl req -newkey rsa:2048 -nodes -subj "/CN=mailpit" -keyout server.key -out server.csr 2>/dev/null
printf "subjectAltName=DNS:mailpit,DNS:localhost\nextendedKeyUsage=serverAuth\n" > ext.cnf
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 30 -extfile ext.cnf -out server.pem 2>/dev/null
chmod 644 ca.pem server.pem server.key
rm -f server.csr ext.cnf ca.srl
echo "test certificates written to deploy/smoke/certs"
