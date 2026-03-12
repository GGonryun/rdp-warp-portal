#!/bin/bash
# Ubuntu VM Setup Script for RDP Broker
# Run: curl -fsSL <raw-url>/setup.sh | sudo bash
# Or:  sudo ./setup.sh

set -euo pipefail

echo "=== RDP Broker Setup ==="

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   echo "This script must be run as root (use sudo)"
   exit 1
fi

# Detect the non-root user who invoked sudo
INSTALL_USER="${SUDO_USER:-$USER}"
INSTALL_HOME=$(eval echo "~$INSTALL_USER")

echo "[1/5] Updating system packages..."
apt-get update
apt-get upgrade -y

echo "[2/5] Installing Docker..."
if command -v docker &> /dev/null; then
    echo "Docker already installed: $(docker --version)"
else
    curl -fsSL https://get.docker.com | sh
    usermod -aG docker "$INSTALL_USER"
    echo "Added $INSTALL_USER to docker group"
fi

echo "[3/5] Installing Docker Compose..."
if docker compose version &> /dev/null 2>&1; then
    echo "Docker Compose already available: $(docker compose version)"
else
    apt-get install -y docker-compose-plugin
fi

echo "[4/5] Installing utilities..."
apt-get install -y curl jq git

echo "[5/5] Verifying installation..."
docker --version
docker compose version

echo ""
echo "=== Setup Complete ==="
echo ""
echo "IMPORTANT: Log out and back in for docker group membership to take effect."
echo ""
echo "Next steps:"
echo "  1. Clone the repo:    git clone <repo-url> ~/rdp-broker && cd ~/rdp-broker"
echo "  2. Create configs:    cp .env.example .env && cp .targets.json.example targets.json"
echo "  3. Edit configs:      nano .env  # Set BROKER_HOST to your public IP"
echo "                        nano targets.json  # Add your target credentials"
echo "  4. Start broker:      docker compose up -d --build"
echo "  5. Verify:            curl http://localhost:8080/health"
echo ""
echo "Required firewall/NSG ports:"
echo "  - 22/TCP        (SSH)"
echo "  - 8080/TCP      (HTTP API)"
echo "  - 33400-33410   (RDP Gatekeeper)"
