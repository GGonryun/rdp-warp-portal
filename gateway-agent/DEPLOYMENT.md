# RDP Bastion Gateway — Deployment Guide

## Prerequisites

- Windows Server 2022 with Administrator access
- Internet access (for ffmpeg download)
- A target Windows host reachable via RDP from the bastion

## Build

On any machine with Go 1.22+:

```bash
cd gateway-agent
GOOS=windows GOARCH=amd64 go build -o bin/gateway-agent.exe ./cmd/agent/
```

The binary embeds all PowerShell scripts — no other files need to be deployed.

## Deploy (one command)

Copy `gateway-agent.exe` to the Windows Server and run as Administrator:

```powershell
.\gateway-agent.exe --install
```

This single command:
- Copies itself to `C:\Gateway\bin\`
- Extracts embedded scripts to `C:\Gateway\scripts\`
- Installs the RDS Session Host role
- Creates session user accounts (gwsession001–020)
- Downloads and installs ffmpeg
- Creates the directory structure under `C:\Gateway` and `D:\recordings`
- Configures RDS policies and firewall rules
- Writes config files and registers the Windows service

A reboot may be required after the RDS role is installed.

### Configure targets

Edit `C:\Gateway\config\credentials.json` with your real target hosts:

```json
{
  "targets": [
    {
      "id": "dc01",
      "name": "Domain Controller",
      "host": "10.1.0.7",
      "port": 3389,
      "username": "rdpadmin",
      "password": "YourPassword",
      "domain": "",
      "tags": ["production"]
    }
  ]
}
```

### Start the service

```powershell
sc start GatewayAgent
```

### Verify

```bash
curl http://bastion:8080/health
curl http://bastion:8080/api/v1/targets
```

## Usage

### Create a session

```bash
curl -X POST http://bastion:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"target_id": "dc01", "requested_by": "admin@company.com"}'
```

The response includes `gateway_user` and `gateway_pass` — use these to RDP into the bastion. The target desktop appears automatically.

### Monitor a live session

Open in a browser: `http://bastion:8080/api/v1/sessions/{session_id}/monitor`

### Download a recording

```bash
curl http://bastion:8080/api/v1/sessions/{session_id}/recording -o recording.mp4
```

## Uninstall

```powershell
gateway-agent.exe --uninstall
```

This removes the service, user accounts, firewall rules, and `C:\Gateway`. Recordings in `D:\recordings` are preserved.
