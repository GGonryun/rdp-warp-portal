// Package secrets provides Azure Workload Identity Federation with Google
// Secret Manager integration. It handles the three-stage token exchange
// (Azure IMDS → Google STS → Google IAM) with in-memory caching.
package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cachedToken holds a token string and its expiry.
type cachedToken struct {
	token     string
	expiresAt time.Time
}

// tokenRefreshBuffer is how long before expiry we proactively refresh.
const tokenRefreshBuffer = 60 * time.Second

// TokenProvider manages the three-stage WIF token exchange with caching.
type TokenProvider struct {
	mu sync.RWMutex

	// Immutable config
	azureCredentialURL string
	wifAudience        string
	serviceAccount     string

	// Cached tokens
	azureToken *cachedToken
	stsToken   *cachedToken
	iamToken   *cachedToken

	httpClient *http.Client
	logger     *slog.Logger

	// Base URLs (overridable for testing)
	stsBaseURL string
	iamBaseURL string
}

// NewTokenProvider creates a TokenProvider for the given WIF configuration.
func NewTokenProvider(azureCredentialURL, wifAudience, serviceAccount string, logger *slog.Logger) *TokenProvider {
	return &TokenProvider{
		azureCredentialURL: azureCredentialURL,
		wifAudience:        wifAudience,
		serviceAccount:     serviceAccount,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger:     logger,
		stsBaseURL: "https://sts.googleapis.com",
		iamBaseURL: "https://iamcredentials.googleapis.com",
	}
}

// GetGoogleAccessToken returns a valid Google access token, refreshing the
// chain as needed. This is the main entry point used by the Secret Manager client.
func (tp *TokenProvider) GetGoogleAccessToken(ctx context.Context) (string, error) {
	return tp.getIAMToken(ctx)
}

// --- Azure IMDS token ---

type azureTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresOn   string `json:"expires_on"` // Unix timestamp as string
	Resource    string `json:"resource"`
	TokenType   string `json:"token_type"`
}

func (tp *TokenProvider) getAzureToken(ctx context.Context) (string, error) {
	tp.mu.RLock()
	if tp.azureToken != nil && time.Now().Before(tp.azureToken.expiresAt.Add(-tokenRefreshBuffer)) {
		token := tp.azureToken.token
		tp.mu.RUnlock()
		return token, nil
	}
	tp.mu.RUnlock()

	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Double-check after acquiring write lock
	if tp.azureToken != nil && time.Now().Before(tp.azureToken.expiresAt.Add(-tokenRefreshBuffer)) {
		return tp.azureToken.token, nil
	}

	tp.logger.Debug("fetching azure IMDS token")

	cached, err := tp.fetchAzureToken(ctx)
	if err != nil {
		return "", fmt.Errorf("azure IMDS token fetch failed: %w", err)
	}
	tp.azureToken = cached

	tp.logger.Debug("azure IMDS token cached", "expires_at", cached.expiresAt.Format(time.RFC3339))
	return cached.token, nil
}

func (tp *TokenProvider) fetchAzureToken(ctx context.Context) (*cachedToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tp.azureCredentialURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Metadata", "true")

	resp, err := tp.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IMDS returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp azureTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in IMDS response")
	}

	expiresOn, err := strconv.ParseInt(tokenResp.ExpiresOn, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse expires_on %q: %w", tokenResp.ExpiresOn, err)
	}

	return &cachedToken{
		token:     tokenResp.AccessToken,
		expiresAt: time.Unix(expiresOn, 0),
	}, nil
}

// --- Google STS token exchange ---

type stsTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds
	TokenType   string `json:"token_type"`
}

func (tp *TokenProvider) getSTSToken(ctx context.Context) (string, error) {
	tp.mu.RLock()
	if tp.stsToken != nil && time.Now().Before(tp.stsToken.expiresAt.Add(-tokenRefreshBuffer)) {
		token := tp.stsToken.token
		tp.mu.RUnlock()
		return token, nil
	}
	tp.mu.RUnlock()

	// Get a fresh Azure token first (outside the write lock to avoid holding
	// the lock during an HTTP call).
	azureToken, err := tp.getAzureToken(ctx)
	if err != nil {
		return "", err
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Double-check
	if tp.stsToken != nil && time.Now().Before(tp.stsToken.expiresAt.Add(-tokenRefreshBuffer)) {
		return tp.stsToken.token, nil
	}

	tp.logger.Debug("exchanging azure token for google STS token")

	cached, err := tp.fetchSTSToken(ctx, azureToken)
	if err != nil {
		return "", fmt.Errorf("google STS exchange failed: %w", err)
	}
	tp.stsToken = cached

	tp.logger.Debug("google STS token cached", "expires_at", cached.expiresAt.Format(time.RFC3339))
	return cached.token, nil
}

func (tp *TokenProvider) fetchSTSToken(ctx context.Context, azureToken string) (*cachedToken, error) {
	form := url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {azureToken},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:jwt"},
		"audience":             {tp.wifAudience},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"scope":                {"https://www.googleapis.com/auth/cloud-platform"},
	}

	u := tp.stsBaseURL + "/v1/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tp.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("STS returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp stsTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in STS response")
	}

	return &cachedToken{
		token:     tokenResp.AccessToken,
		expiresAt: time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}, nil
}

// --- Google IAM service account impersonation ---

type iamGenerateTokenRequest struct {
	Scope    []string `json:"scope"`
	Lifetime string   `json:"lifetime"`
}

type iamGenerateTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireTime  string `json:"expireTime"` // RFC3339
}

func (tp *TokenProvider) getIAMToken(ctx context.Context) (string, error) {
	tp.mu.RLock()
	if tp.iamToken != nil && time.Now().Before(tp.iamToken.expiresAt.Add(-tokenRefreshBuffer)) {
		token := tp.iamToken.token
		tp.mu.RUnlock()
		return token, nil
	}
	tp.mu.RUnlock()

	// Get STS token first (outside write lock).
	stsToken, err := tp.getSTSToken(ctx)
	if err != nil {
		return "", err
	}

	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Double-check
	if tp.iamToken != nil && time.Now().Before(tp.iamToken.expiresAt.Add(-tokenRefreshBuffer)) {
		return tp.iamToken.token, nil
	}

	tp.logger.Debug("impersonating google service account", "service_account", tp.serviceAccount)

	cached, err := tp.fetchIAMToken(ctx, stsToken)
	if err != nil {
		return "", fmt.Errorf("google IAM impersonation failed: %w", err)
	}
	tp.iamToken = cached

	tp.logger.Debug("google IAM token cached", "expires_at", cached.expiresAt.Format(time.RFC3339))
	return cached.token, nil
}

func (tp *TokenProvider) fetchIAMToken(ctx context.Context, stsToken string) (*cachedToken, error) {
	u := fmt.Sprintf(
		"%s/v1/projects/-/serviceAccounts/%s:generateAccessToken",
		tp.iamBaseURL,
		tp.serviceAccount,
	)

	reqBody := iamGenerateTokenRequest{
		Scope:    []string{"https://www.googleapis.com/auth/cloud-platform"},
		Lifetime: "3600s",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+stsToken)

	resp, err := tp.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IAM returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp iamGenerateTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty accessToken in IAM response")
	}

	expireTime, err := time.Parse(time.RFC3339, tokenResp.ExpireTime)
	if err != nil {
		return nil, fmt.Errorf("parse expireTime %q: %w", tokenResp.ExpireTime, err)
	}

	return &cachedToken{
		token:     tokenResp.AccessToken,
		expiresAt: expireTime,
	}, nil
}
