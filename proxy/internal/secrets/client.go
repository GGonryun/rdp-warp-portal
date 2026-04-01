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
	projectID  string

	// Base URL (overridable for testing)
	secretManagerBaseURL string
}

// NewClient creates a Secret Manager client backed by the given TokenProvider.
// If projectID is non-empty, short secret names (e.g. "my-secret") are
// automatically expanded to "projects/{projectID}/secrets/my-secret".
func NewClient(tokens *TokenProvider, projectID string, logger *slog.Logger) *Client {
	return &Client{
		tokens: tokens,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger:               logger,
		projectID:            projectID,
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

	// Expand short secret names using the configured project ID.
	path := secretName
	if !strings.HasPrefix(path, "projects/") {
		if c.projectID == "" {
			return "", fmt.Errorf("secret %q is a short name but GCP_PROJECT_ID is not configured", secretName)
		}
		path = fmt.Sprintf("projects/%s/secrets/%s", c.projectID, path)
	}
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

	value := string(decoded)
	// Log the resolved version and a preview of the value for debugging.
	preview := value
	if len(preview) > 4 {
		preview = preview[:4] + "***"
	}
	c.logger.Info("secret accessed",
		"name", secretName,
		"resolved_version", secretResp.Name,
		"value_length", len(value),
		"value_preview", preview,
	)
	return value, nil
}
