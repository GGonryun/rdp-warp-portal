#!/bin/bash
# Ubuntu VM Setup Script for RDP Broker
# Usage: sudo ./setup.sh

set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

echo "=== RDP Broker Setup ==="

if [[ $EUID -ne 0 ]]; then
   echo "This script must be run as root (use sudo)"
   exit 1
fi

INSTALL_USER="${SUDO_USER:-$USER}"

echo "[1/4] Installing dependencies..."
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg

echo "[2/4] Installing Docker..."
if command -v docker &> /dev/null; then
    echo "Docker already installed: $(docker --version)"
else
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
    apt-get update -qq
    apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    usermod -aG docker "$INSTALL_USER"
    echo "Added $INSTALL_USER to docker group"
fi

echo "[3/4] Starting Docker..."
systemctl enable docker
systemctl start docker

echo "[4/4] Verifying..."
docker --version
docker compose version

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Log out and back in for docker group to take effect, then:"
echo ""
echo "  cp .env.example .env && cp proxy/targets.json.example proxy/targets.json"
echo "  nano .env                  # Set BROKER_HOST"
echo "  nano proxy/targets.json    # Add credentials"
echo "  docker compose up -d --build"
echo ""
echo "Required ports: 22, 8080, 33400-33500"
