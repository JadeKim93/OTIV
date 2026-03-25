#!/bin/sh
set -e

# Ensure TUN device exists
mkdir -p /dev/net
if [ ! -c /dev/net/tun ]; then
    mknod /dev/net/tun c 10 200
fi

exec openvpn --config /etc/openvpn/server.conf
