#!/bin/sh
mkdir -p /var/lib/eightly-agent
systemctl daemon-reload
echo "Eight.ly Agent installed. Configure with environment variables in /etc/systemd/system/eightly-agent.service"
echo "Then: systemctl enable --now eightly-agent"
