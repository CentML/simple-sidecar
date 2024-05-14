#!/bin/bash 

# Exit on error
set -e

# Variables
CA_KEY="ca.key"
CA_CERT="ca.crt"
SERVER_KEY="server.key"
SERVER_CSR="server.csr"
SERVER_CERT="server.crt"
OPENSSL_CONF="openssl.cnf"

# Get the namespace from the first command line argument, default to 'simple-sidecar'
NAMESPACE=${1:-simple-sidecar}

# Get the secret name from the second command line argument, default to 'simple-sidecar'
SECRET_NAME=${2:-simple-sidecar-tls}

# Get the service name from the third command line argument, default to 'simple-sidecar-t'
SERVICE_NAME=${3:-simple-sidecar}

# Create OpenSSL configuration file with DNS names
cat > $OPENSSL_CONF <<EOF
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req

[req_distinguished_name]

[v3_req]
subjectAltName = @alt_names

[alt_names]
DNS.1 = $SERVICE_NAME
DNS.2 = $SERVICE_NAME.$NAMESPACE
DNS.3 = $SERVICE_NAME.$NAMESPACE.svc
EOF

# Generate CA private key
openssl genrsa -out $CA_KEY 2048

# Generate CA certificate
openssl req -x509 -new -nodes -key $CA_KEY -sha256 -days 365 -subj "/CN=myca" -out $CA_CERT

# Generate server private key
openssl genrsa -out $SERVER_KEY 2048

# Generate server CSR with a subject that matches service-name
openssl req -new -key $SERVER_KEY -subj "/CN=$SERVICE_NAME.$NAMESPACE.svc" -config $OPENSSL_CONF -out $SERVER_CSR

# Generate server certificate
openssl x509 -req -in $SERVER_CSR -CA $CA_CERT -CAkey $CA_KEY -CAcreateserial -out $SERVER_CERT -days 365 -sha256 -extensions v3_req -extfile $OPENSSL_CONF

# Delete the secret if it already exists
echo -e "\n\nHere's the command to delete the secret if it already exists:\n"
echo -e "\tkubectl delete secret $SECRET_NAME --namespace $NAMESPACE --ignore-not-found\n"

# Create Kubernetes secret with the server certificate, server key, and CA certificate
echo -e "\n\nHere's the command to create the secret with the server certificate, server key, and CA certificate:\n"
echo -e "\tkubectl create secret generic $SECRET_NAME --from-file=tls.crt=$SERVER_CERT --from-file=tls.key=$SERVER_KEY --from-file=ca.crt=$CA_CERT --namespace $NAMESPACE\n"

B64_CA_CERT=`cat $CA_CERT | base64`
echo -e "\n\nHere's the CA cert in base64 format for your values.yaml file:\n"
echo -e caBundle: $B64_CA_CERT

# Cleanup CSR, CA serial and OpenSSL configuration file
rm $SERVER_CSR
rm ca.srl
rm $OPENSSL_CONF