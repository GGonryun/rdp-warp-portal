package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ErrRecordingGone is returned when the broker responds with 404 for a
// recording, indicating the recording has been deleted or the session was
// terminated. Callers should stop sending data for this recording.
var ErrRecordingGone = errors.New("recording not found on broker")

// Client is an HTTP client for the proxy recording API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new API client.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// HealthCheck verifies the agent can reach the broker by hitting /health.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to broker: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("broker returned status %d", resp.StatusCode)
	}

	return nil
}

// CreateRecordingRequest is the payload for creating a new recording.
type CreateRecordingRequest struct {
	SessionID     string `json:"session_id"`
	TargetID      string `json:"target_id"`
	TargetName    string `json:"target_name"`
	WindowsUser   string `json:"windows_user"`
	ProxyUser     string `json:"proxy_user"`
	AgentHostname string `json:"agent_hostname"`
	ChunkSecs     int    `json:"chunk_secs,omitempty"`
}

// Recording represents a recording returned by the server.
type Recording struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

// RecordingEvent represents a captured event to send to the server.
type RecordingEvent struct {
	Timestamp time.Time      `json:"ts"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
}

// doWithRetry executes an HTTP request with exponential backoff retry.
// It retries up to 3 attempts with delays of 1s, 2s, and 4s.
func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			slog.Info("retrying request", "attempt", attempt+1, "url", req.URL.String())
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delays[attempt-1]):
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("request failed after 3 attempts: %w", lastErr)
}

// newRequest creates an HTTP request with auth header if configured.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

// CreateRecording creates a new recording on the server.
func (c *Client) CreateRecording(ctx context.Context, req CreateRecordingRequest) (*Recording, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/recordings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithRetry(ctx, httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create recording failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var recording Recording
	if err := json.NewDecoder(resp.Body).Decode(&recording); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &recording, nil
}

// UploadChunk uploads a video chunk for a recording.
func (c *Client) UploadChunk(ctx context.Context, recordingID string, data io.Reader) error {
	path := fmt.Sprintf("/api/recordings/%s/chunks", recordingID)
	httpReq, err := c.newRequest(ctx, http.MethodPost, path, data)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "video/mp2t")

	resp, err := c.doWithRetry(ctx, httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrRecordingGone
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload chunk failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendEvents sends a batch of recording events to the server.
func (c *Client) SendEvents(ctx context.Context, recordingID string, events []RecordingEvent) error {
	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}

	path := fmt.Sprintf("/api/recordings/%s/events", recordingID)
	httpReq, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithRetry(ctx, httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrRecordingGone
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send events failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// EndRecording signals the end of a recording.
func (c *Client) EndRecording(ctx context.Context, recordingID string) error {
	path := fmt.Sprintf("/api/recordings/%s/end", recordingID)
	httpReq, err := c.newRequest(ctx, http.MethodPut, path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.doWithRetry(ctx, httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrRecordingGone
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("end recording failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
