package api

import (
	"context"
	"encoding/json"
	"net/http"
)

// SecretReader defines the interface for reading secrets.
type SecretReader interface {
	AccessSecret(ctx context.Context, secretName string) (string, error)
}

// SecretsHandler handles secret testing endpoints.
type SecretsHandler struct {
	client            SecretReader
	azureCredentialURL  string
	gcpServiceAccount string
}

// NewSecretsHandler creates a new secrets handler.
// client may be nil if WIF is not configured.
func NewSecretsHandler(client SecretReader, azureCredentialURL, gcpServiceAccount string) *SecretsHandler {
	return &SecretsHandler{
		client:            client,
		azureCredentialURL:  azureCredentialURL,
		gcpServiceAccount: gcpServiceAccount,
	}
}

// RegisterRoutes registers the secrets routes on the router.
func (h *SecretsHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("POST /api/secrets/access", h.Access, true)
	router.HandleFunc("GET /api/secrets/status", h.Status, true)
}

// AccessSecretRequest is the request body for reading a secret.
type AccessSecretRequest struct {
	SecretName string `json:"secret_name"`
}

// AccessSecretResponse is the response body for reading a secret.
type AccessSecretResponse struct {
	SecretName string `json:"secret_name"`
	Value      string `json:"value"`
	Length     int    `json:"length"`
}

// Access handles POST /api/secrets/access.
func (h *SecretsHandler) Access(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "WIF not configured — set AZURE_CREDENTIAL_URL, GCP_WIF_AUDIENCE, and GCP_SERVICE_ACCOUNT")
		return
	}

	var req AccessSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SecretName == "" {
		writeError(w, http.StatusBadRequest, "secret_name is required")
		return
	}

	value, err := h.client.AccessSecret(r.Context(), req.SecretName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, AccessSecretResponse{
		SecretName: req.SecretName,
		Value:      value,
		Length:     len(value),
	})
}

// SecretsStatusResponse is the response body for the status endpoint.
type SecretsStatusResponse struct {
	Configured        bool   `json:"configured"`
	AzureCredentialURL  string `json:"azure_credential_url,omitempty"`
	GCPServiceAccount string `json:"gcp_service_account,omitempty"`
}

// Status handles GET /api/secrets/status.
func (h *SecretsHandler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, SecretsStatusResponse{
		Configured:        h.client != nil,
		AzureCredentialURL:  h.azureCredentialURL,
		GCPServiceAccount: h.gcpServiceAccount,
	})
}
