#!/bin/sh
if ! id eightly-agent >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin eightly-agent
  usermod -aG docker eightly-agent 2>/dev/null || true
fi
mkdir -p /var/lib/eightly-agent
chown eightly-agent:eightly-agent /var/lib/eightly-agent
systemctl daemon-reload
echo "Eight.ly Agent installed."
echo "Configure: sudo systemctl edit eightly-agent"
echo "Start:     sudo systemctl enable --now eightly-agent"
