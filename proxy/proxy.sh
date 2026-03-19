#!/usr/bin/env bash
# p0rtal broker installer and service manager for Ubuntu Linux.
# Usage: sudo ./proxy.sh <command>
#
# Commands:
#   install     Install Docker, copy files to /opt/p0rtal, create systemd service, start
#   uninstall   Stop service, remove systemd unit, tear down containers, remove /opt/p0rtal
#   reinstall   Uninstall then install
#   start       Start the service
#   stop        Stop the service
#   status      Show service and container status
#   log|logs    Tail broker container logs
#   update      Rebuild and restart containers

set -euo pipefail

INSTALL_DIR="/opt/p0rtal"
SERVICE_NAME="p0rtal"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ---------- helpers ----------

require_root() {
  if [[ $EUID -ne 0 ]]; then
    echo "Error: this command must be run as root (use sudo)"
    exit 1
  fi
}

install_docker() {
  if command -v docker &>/dev/null; then
    echo "Docker already installed: $(docker --version)"
    return
  fi

  echo "Installing Docker..."
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg

  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg

  echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
    $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list

  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

  systemctl enable docker
  systemctl start docker

  if [ -n "${SUDO_USER:-}" ]; then
    usermod -aG docker "$SUDO_USER"
    echo "Added $SUDO_USER to docker group (re-login to take effect)"
  fi

  echo "Docker installed: $(docker --version)"
}

copy_files() {
  echo "Copying files to ${INSTALL_DIR}..."
  mkdir -p "${INSTALL_DIR}"

  # Copy project files needed for docker compose build.
  cp -r "${SCRIPT_DIR}" "${INSTALL_DIR}/proxy"
  cp -r "${PROJECT_DIR}/web" "${INSTALL_DIR}/web"
  cp "${PROJECT_DIR}/docker-compose.yml" "${INSTALL_DIR}/docker-compose.yml"

  # Copy .env (prefer existing, then .env, then .env.example).
  if [ -f "${INSTALL_DIR}/.env" ]; then
    echo "  Keeping existing .env"
  elif [ -f "${PROJECT_DIR}/.env" ]; then
    cp "${PROJECT_DIR}/.env" "${INSTALL_DIR}/.env"
    echo "  Copied .env"
  elif [ -f "${PROJECT_DIR}/.env.example" ]; then
    cp "${PROJECT_DIR}/.env.example" "${INSTALL_DIR}/.env"
    echo "  Copied .env.example as .env — edit it with your BROKER_HOST and API_KEY"
  fi

  # Copy targets.json if not already present in install dir.
  if [ -f "${INSTALL_DIR}/proxy/targets.json" ]; then
    echo "  Keeping existing proxy/targets.json"
  elif [ -f "${SCRIPT_DIR}/targets.json" ]; then
    echo "  Copied proxy/targets.json"
  elif [ -f "${SCRIPT_DIR}/targets.json.example" ]; then
    cp "${SCRIPT_DIR}/targets.json.example" "${INSTALL_DIR}/proxy/targets.json"
    echo "  Copied targets.json.example — edit it with your target credentials"
  fi

  # Create recordings data directory.
  mkdir -p "${INSTALL_DIR}/data/recordings"

  # Copy this script and create a symlink on PATH.
  cp "${SCRIPT_DIR}/proxy.sh" "${INSTALL_DIR}/proxy.sh"
  chmod +x "${INSTALL_DIR}/proxy.sh"
  ln -sf "${INSTALL_DIR}/proxy.sh" /usr/local/bin/p0rtal

  echo "Files copied to ${INSTALL_DIR}"
}

create_service() {
  echo "Creating systemd service..."
  cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=p0rtal RDP Proxy Broker
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${INSTALL_DIR}
ExecStart=/usr/bin/docker compose up -d --build
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  echo "Systemd service created and enabled"
}

# ---------- commands ----------

cmd_install() {
  require_root
  echo "=== Installing p0rtal broker ==="

  install_docker
  copy_files
  create_service

  echo "Starting service..."
  systemctl start "${SERVICE_NAME}"

  echo ""
  echo "=== Installation complete ==="
  echo ""
  echo "Service is running. Manage with:"
  echo "  sudo p0rtal status"
  echo "  sudo p0rtal log"
  echo "  sudo p0rtal stop"
  echo "  sudo p0rtal start"
  echo ""
  echo "Config files:"
  echo "  ${INSTALL_DIR}/.env                  (BROKER_HOST, API_KEY)"
  echo "  ${INSTALL_DIR}/proxy/targets.json    (RDP target credentials)"
  echo ""
  echo "Required ports: 8080, 33400-33500"
}

cmd_uninstall() {
  require_root
  echo "=== Uninstalling p0rtal broker ==="

  # Stop and disable service.
  if systemctl is-active "${SERVICE_NAME}" &>/dev/null; then
    echo "Stopping service..."
    systemctl stop "${SERVICE_NAME}"
  fi
  if systemctl is-enabled "${SERVICE_NAME}" &>/dev/null; then
    systemctl disable "${SERVICE_NAME}"
  fi

  # Tear down containers.
  if [ -f "${INSTALL_DIR}/docker-compose.yml" ]; then
    echo "Removing containers..."
    (cd "${INSTALL_DIR}" && docker compose down --rmi local --volumes 2>/dev/null || true)
  fi

  # Remove systemd unit.
  if [ -f "${SERVICE_FILE}" ]; then
    rm -f "${SERVICE_FILE}"
    systemctl daemon-reload
  fi

  # Remove symlink.
  rm -f /usr/local/bin/p0rtal

  # Remove install directory.
  if [ -d "${INSTALL_DIR}" ]; then
    echo "Removing ${INSTALL_DIR}..."
    rm -rf "${INSTALL_DIR}"
  fi

  echo "=== Uninstall complete ==="
}

cmd_reinstall() {
  require_root
  echo "=== Reinstalling p0rtal broker ==="
  cmd_uninstall
  cmd_install
}

cmd_start() {
  require_root
  systemctl start "${SERVICE_NAME}"
  echo "Service started"
}

cmd_stop() {
  require_root
  systemctl stop "${SERVICE_NAME}"
  echo "Service stopped"
}

cmd_status() {
  echo "=== Service Status ==="
  systemctl status "${SERVICE_NAME}" --no-pager 2>/dev/null || true
  echo ""
  echo "=== Containers ==="
  if [ -f "${INSTALL_DIR}/docker-compose.yml" ]; then
    (cd "${INSTALL_DIR}" && docker compose ps 2>/dev/null || true)
  else
    echo "Not installed"
  fi
}

cmd_log() {
  if [ ! -f "${INSTALL_DIR}/docker-compose.yml" ]; then
    echo "Error: p0rtal is not installed. Run: sudo ./proxy.sh install"
    exit 1
  fi
  (cd "${INSTALL_DIR}" && docker compose logs -f --tail 100)
}

cmd_update() {
  require_root
  echo "=== Updating p0rtal broker ==="

  copy_files

  echo "Rebuilding and restarting containers..."
  (cd "${INSTALL_DIR}" && docker compose up -d --build)

  echo "=== Update complete ==="
}

# ---------- main ----------

case "${1:-}" in
  install)    cmd_install ;;
  uninstall)  cmd_uninstall ;;
  reinstall)  cmd_reinstall ;;
  start)      cmd_start ;;
  stop)       cmd_stop ;;
  status)     cmd_status ;;
  log|logs)   cmd_log ;;
  update)     cmd_update ;;
  *)
    echo "p0rtal broker service manager"
    echo ""
    echo "Usage: sudo $0 <command>"
    echo ""
    echo "Commands:"
    echo "  install     Install Docker, deploy to /opt/p0rtal, create systemd service"
    echo "  uninstall   Remove service, containers, and /opt/p0rtal"
    echo "  reinstall   Uninstall and reinstall"
    echo "  start       Start the service"
    echo "  stop        Stop the service"
    echo "  status      Show service and container status"
    echo "  log         Tail broker logs"
    echo "  update      Copy latest files, rebuild and restart"
    echo ""
    exit 1
    ;;
esac
