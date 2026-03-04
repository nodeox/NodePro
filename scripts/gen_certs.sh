#!/bin/bash

# 设置证书存储目录
CERT_DIR="./configs/certs"
mkdir -p $CERT_DIR

# 创建 OpenSSL 配置文件以包含 SAN
CAT_CONFIG=$CERT_DIR/openssl.conf
cat > $CAT_CONFIG <<EOF
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req
[req_distinguished_name]
[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
subjectAltName = @alt_names
[alt_names]
DNS.1 = localhost
IP.1 = 127.0.0.1
EOF

echo "Creating Certificate Authority..."
openssl genrsa -out $CERT_DIR/ca.key 4096
openssl req -x509 -new -nodes -key $CERT_DIR/ca.key -sha256 -days 3650 \
    -out $CERT_DIR/ca.crt -subj "/CN=NodePass-CA"

echo "Creating Server (Controller/Relay) Certificate..."
openssl genrsa -out $CERT_DIR/server.key 2048
openssl req -new -key $CERT_DIR/server.key -out $CERT_DIR/server.csr \
    -subj "/CN=localhost" -config $CAT_CONFIG
openssl x509 -req -in $CERT_DIR/server.csr -CA $CERT_DIR/ca.crt -CAkey $CERT_DIR/ca.key \
    -CAcreateserial -out $CERT_DIR/server.crt -days 365 -sha256 -extfile $CAT_CONFIG -extensions v3_req

echo "Creating Client (Agent) Certificate..."
openssl genrsa -out $CERT_DIR/client.key 2048
openssl req -new -key $CERT_DIR/client.key -out $CERT_DIR/client.csr \
    -subj "/CN=nodepass-agent" -config $CAT_CONFIG
openssl x509 -req -in $CERT_DIR/client.csr -CA $CERT_DIR/ca.crt -CAkey $CERT_DIR/ca.key \
    -CAcreateserial -out $CERT_DIR/client.crt -days 365 -sha256 -extfile $CAT_CONFIG -extensions v3_req

# 清理 CSR 文件
rm $CERT_DIR/*.csr
rm $CERT_DIR/*.srl
rm $CAT_CONFIG

echo "Certificates generated in $CERT_DIR"
