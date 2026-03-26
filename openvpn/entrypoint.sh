#!/bin/sh
set -e

# Ensure TUN device exists
mkdir -p /dev/net
if [ ! -c /dev/net/tun ]; then
    mknod /dev/net/tun c 10 200
fi

# Start OpenVPN in background; we need it to create tun0 before starting dnsmasq
openvpn --config /etc/openvpn/server.conf &
OVPN_PID=$!

# Wait for tun0 to appear (up to 30 seconds)
i=0
while ! ip link show tun0 >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 60 ]; then
        echo "tun0 did not appear in time" >&2
        kill "$OVPN_PID" 2>/dev/null
        exit 1
    fi
    sleep 0.5
done

# Start dnsmasq listening only on the VPN tunnel interface.
# --no-resolv: don't forward to upstream; only resolve .vpn.local locally.
# --local: authoritative for vpn.local, return NXDOMAIN for everything else.
dnsmasq \
    --no-daemon \
    --no-resolv \
    --interface=tun0 \
    --bind-interfaces \
    --addn-hosts=/etc/openvpn/dnsmasq.hosts \
    --domain=vpn.local \
    --local=/vpn.local/ \
    --pid-file=/var/run/dnsmasq.pid &

# Wait for OpenVPN to exit; bring down dnsmasq with it
wait "$OVPN_PID"
