#!/bin/bash
# Local testing script for RDP Broker
# This script starts the broker and creates a test session

set -e

BROKER_PORT=${BROKER_PORT:-8080}
TARGET_ID=${TARGET_ID:-dc-01}  # Mock targets: dc-01, ws-05

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}=== RDP Broker Local Test ===${NC}"

# Check if broker is already running
if curl -s http://localhost:$BROKER_PORT/health > /dev/null 2>&1; then
    echo -e "${GREEN}Broker already running on port $BROKER_PORT${NC}"
else
    echo -e "${YELLOW}Starting broker...${NC}"
    echo "Run this in another terminal:"
    echo ""
    echo "  ALLOW_MOCK_PROVIDER=true ALLOW_INSECURE_AUTH=true go run ./cmd/broker"
    echo ""
    echo "Then re-run this script."
    exit 1
fi

# Create a simple JWT token (dev mode accepts any token)
# Format: header.payload.signature (all base64url encoded)
JWT_HEADER=$(echo -n '{"alg":"HS256","typ":"JWT"}' | base64 | tr -d '=' | tr '/+' '_-')
JWT_PAYLOAD=$(echo -n '{"sub":"test-user","exp":9999999999}' | base64 | tr -d '=' | tr '/+' '_-')
JWT_TOKEN="${JWT_HEADER}.${JWT_PAYLOAD}.fake-signature"

echo -e "${GREEN}Using JWT token for user: test-user${NC}"

# Create a session
echo -e "\n${YELLOW}Creating session for target: $TARGET_ID${NC}"

RESPONSE=$(curl -s -X POST http://localhost:$BROKER_PORT/api/sessions \
    -H "Authorization: Bearer $JWT_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"target_id\": \"$TARGET_ID\"}")

echo "Response: $RESPONSE"

# Extract session ID using grep/sed (portable)
SESSION_ID=$(echo "$RESPONSE" | grep -o '"session_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$SESSION_ID" ]; then
    echo -e "${RED}Failed to create session${NC}"
    exit 1
fi

echo -e "${GREEN}Session created: $SESSION_ID${NC}"

# Download RDP file
RDP_FILE="/tmp/rdp-broker-test-${TARGET_ID}.rdp"
echo -e "\n${YELLOW}Downloading RDP file...${NC}"

curl -s -o "$RDP_FILE" \
    -H "Authorization: Bearer $JWT_TOKEN" \
    "http://localhost:$BROKER_PORT/api/sessions/$SESSION_ID/rdp"

echo -e "${GREEN}RDP file saved to: $RDP_FILE${NC}"
echo ""
echo "=== RDP File Contents ==="
cat "$RDP_FILE"
echo ""
echo "========================="

# Show connection info
PROXY_PORT=$(echo "$RESPONSE" | grep -o '"proxy_port":[0-9]*' | cut -d':' -f2)
TOKEN_EXPIRES=$(echo "$RESPONSE" | grep -o '"token_expires_at":"[^"]*"' | cut -d'"' -f4)

echo ""
echo -e "${GREEN}=== Connection Info ===${NC}"
echo "Proxy Host: localhost"
echo "Proxy Port: $PROXY_PORT"
echo "Token Expires: $TOKEN_EXPIRES"
echo ""
echo -e "${YELLOW}To connect:${NC}"
echo "  macOS:   open '$RDP_FILE'"
echo "  Linux:   xfreerdp '$RDP_FILE'"
echo "  Windows: double-click the .rdp file"
echo ""
echo -e "${YELLOW}Note:${NC} The token expires in 60 seconds. Connect quickly!"
echo ""
echo -e "${YELLOW}To clean up:${NC}"
echo "  curl -X DELETE -H 'Authorization: Bearer $JWT_TOKEN' http://localhost:$BROKER_PORT/api/sessions/$SESSION_ID"
