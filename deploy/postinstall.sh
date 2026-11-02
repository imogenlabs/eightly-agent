#!/bin/sh
if ! id eightly-agent >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin eightly-agent
  usermod -aG docker eightly-agent 2>/dev/null || true
fi
mkdir -p /var/lib/eightly-agent
chown eightly-agent:eightly-agent /var/lib/eightly-agent
chmod 0700 /var/lib/eightly-agent

# The agent's secrets live here, readable by root only; systemd reads the file
# before it starts the agent. An existing file is the operator's and is never
# replaced, only its mode corrected.
install -d -m 0700 -o root -g root /etc/eightly-agent
if [ ! -e /etc/eightly-agent/agent.env ]; then
  (
    umask 077
    cat > /etc/eightly-agent/agent.env <<'ENV'
# Eight.ly Agent settings. Keep this file root-owned and mode 0600.
# Restart after editing: sudo systemctl restart eightly-agent
#
# Eight.ly OS shows these values under Environments -> Add host.
# Uncomment and fill in. A line set here overrides the same variable set
# anywhere in the unit or a drop-in.
#EIGHTLY_AGENT_SECRET=
#EIGHTLY_SERVER_URL=
#EIGHTLY_ENROLL_TOKEN=
ENV
  )
fi
chown root:root /etc/eightly-agent/agent.env
chmod 0600 /etc/eightly-agent/agent.env

systemctl daemon-reload
echo "Eight.ly Agent installed."
echo "Configure: sudoedit /etc/eightly-agent/agent.env"
echo "Start:     sudo systemctl enable --now eightly-agent"
