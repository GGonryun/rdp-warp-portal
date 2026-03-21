package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client provides access to Google Secret Manager via WIF.
type Client struct {
	tokens     *TokenProvider
	httpClient *http.Client
	logger     *slog.Logger

	// Base URL (overridable for testing)
	secretManagerBaseURL string
}

// NewClient creates a Secret Manager client backed by the given TokenProvider.
func NewClient(tokens *TokenProvider, logger *slog.Logger) *Client {
	return &Client{
		tokens: tokens,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger:               logger,
		secretManagerBaseURL: "https://secretmanager.googleapis.com",
	}
}

type secretAccessResponse struct {
	Name    string `json:"name"`
	Payload struct {
		Data string `json:"data"` // base64-encoded
	} `json:"payload"`
}

// AccessSecret reads the latest version of a secret from Google Secret Manager.
//
// secretName should be the full resource name, e.g.:
//
//	projects/233195464130/secrets/my-secret
//
// The method appends /versions/latest:access if not already present.
func (c *Client) AccessSecret(ctx context.Context, secretName string) (string, error) {
	token, err := c.tokens.GetGoogleAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("get access token: %w", err)
	}

	// Normalize the resource path
	path := secretName
	if !strings.Contains(path, "/versions/") {
		path += "/versions/latest"
	}
	if !strings.HasSuffix(path, ":access") {
		path += ":access"
	}

	u := fmt.Sprintf("%s/v1/%s", c.secretManagerBaseURL, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("secret manager returned %d: %s", resp.StatusCode, body)
	}

	var secretResp secretAccessResponse
	if err := json.Unmarshal(body, &secretResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(secretResp.Payload.Data)
	if err != nil {
		// Try URL-safe encoding as fallback
		decoded, err = base64.URLEncoding.DecodeString(secretResp.Payload.Data)
		if err != nil {
			return "", fmt.Errorf("decode secret payload: %w", err)
		}
	}

	c.logger.Debug("secret accessed", "name", secretName, "bytes", len(decoded))
	return string(decoded), nil
}
