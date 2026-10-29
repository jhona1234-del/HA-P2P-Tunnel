#!/bin/sh
echo "Iniciando Túnel P2P do Home Assistant..."
mkdir -p /data
exec /usr/bin/p2p-server -key /data/peer.key
