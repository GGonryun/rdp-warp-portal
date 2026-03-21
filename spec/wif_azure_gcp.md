# Azure VM → GCP Workload Identity Federation (WIF) Spec

## Overview

Workload Identity Federation lets an Azure VM access GCP resources (e.g. Secret Manager) **without** a GCP service account key. The Azure VM gets a managed identity token from Azure IMDS, exchanges it with GCP STS for a federated token, then impersonates a GCP service account.

**Token flow:**

```
Azure IMDS (169.254.169.254)
  → Azure AD access token (scoped to Entra ID app)
    → GCP STS (sts.googleapis.com) exchanges it for a federated token
      → GCP IAM Credentials API impersonates a service account
        → Short-lived GCP access token with SA permissions
```

Google client libraries handle this entire chain transparently when `GOOGLE_APPLICATION_CREDENTIALS` points at the credential config file.

---

## Prerequisites

- An Azure VM with a user-assigned managed identity attached
- A GCP project with Secret Manager API (or whatever API you need) enabled
- `gcloud` CLI authenticated with permissions to create workload identity pools and service accounts

---

## Step-by-step setup

### Azure side

#### 1. Register an Entra ID application

- Azure Portal → Entra ID → App Registrations → New Registration
- Name: e.g. `gcp-wif-app`
- Account type: Single tenant
- Save the **Application (client) ID** and **Directory (tenant) ID**

#### 2. Set an Application ID URI

- App Registration → Expose an API → Set Application ID URI
- Use the default: `api://<client-id>` (e.g. `api://8d964ed5-c223-44f8-b0e0-e7d40687bb1b`)
- This URI is the `audience` / `resource` the managed identity requests tokens for

#### 3. Create a user-assigned managed identity

- Azure Portal → Managed Identities → Create
- Note the **Object (principal) ID** — this is the `subject` in GCP's attribute mapping

#### 4. Assign the managed identity to the VM

- VM → Security → Identity → User Assigned → Add → select the managed identity

#### 5. (Optional but recommended) Lock down token issuance

By default, **any** identity in the tenant can request tokens for the app. To restrict:

- Entra ID → Enterprise Applications → find the app
- Properties → set **"Assignment required?"** to **Yes**
- Users and groups → Add user/group → search for the managed identity
- Role: **"Default Access"** (built-in, no custom role needed)

If "Assignment required" is left as No (default), skip this step — all tenant identities can get tokens.

### GCP side

#### 6. Create a workload identity pool

```bash
gcloud iam workload-identity-pools create <POOL_ID> \
  --location="global" \
  --display-name="Azure Pool"
```

#### 7. Create an Azure OIDC provider in the pool

```bash
gcloud iam workload-identity-pools providers create-oidc <PROVIDER_ID> \
  --location="global" \
  --workload-identity-pool=<POOL_ID> \
  --issuer-uri="https://sts.windows.net/<AZURE_TENANT_ID>/" \
  --allowed-audiences="<APPLICATION_ID_URI>" \
  --attribute-mapping="google.subject=assertion.sub"
```

- `issuer-uri`: Always `https://sts.windows.net/<tenant-id>/` (trailing slash required)
- `allowed-audiences`: The Application ID URI from step 2 (e.g. `api://8d964ed5-...`)
- `attribute-mapping`: Maps the Azure token's `sub` claim (the managed identity Object ID) to `google.subject`

#### 8. Create a GCP service account for the Azure workload to impersonate

```bash
gcloud iam service-accounts create <SA_NAME> \
  --display-name="Azure WIF SA"
```

#### 9. Grant the service account access to GCP resources

Example for Secret Manager:

```bash
gcloud projects add-iam-policy-binding <GCP_PROJECT_ID> \
  --member="serviceAccount:<SA_NAME>@<GCP_PROJECT_ID>.iam.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

#### 10. Grant the federated principal permission to impersonate the SA

```bash
gcloud iam service-accounts add-iam-policy-binding \
  <SA_NAME>@<GCP_PROJECT_ID>.iam.gserviceaccount.com \
  --role="roles/iam.workloadIdentityUser" \
  --member="principal://iam.googleapis.com/projects/<GCP_PROJECT_NUMBER>/locations/global/workloadIdentityPools/<POOL_ID>/subject/<MANAGED_IDENTITY_OBJECT_ID>"
```

- `<GCP_PROJECT_NUMBER>`: Get via `gcloud projects describe <PROJECT_ID> --format="value(projectNumber)"`
- `<MANAGED_IDENTITY_OBJECT_ID>`: The Object (principal) ID from step 3

#### 11. Generate the credential config file

```bash
gcloud iam workload-identity-pools create-cred-config \
  projects/<GCP_PROJECT_NUMBER>/locations/global/workloadIdentityPools/<POOL_ID>/providers/<PROVIDER_ID> \
  --service-account=<SA_NAME>@<GCP_PROJECT_ID>.iam.gserviceaccount.com \
  --azure \
  --app-id-uri="<APPLICATION_ID_URI>" \
  --output-file=gcp-creds.json
```

This produces a JSON file like:

```json
{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/<NUM>/locations/global/workloadIdentityPools/<POOL>/providers/<PROVIDER>",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://sts.googleapis.com/v1/token",
  "credential_source": {
    "url": "http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&resource=<APPLICATION_ID_URI>",
    "headers": { "Metadata": "True" },
    "format": {
      "type": "json",
      "subject_token_field_name": "access_token"
    }
  },
  "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/<SA>@<PROJECT>.iam.gserviceaccount.com:generateAccessToken"
}
```

The `credential_source.url` is the Azure IMDS managed identity token endpoint — **not** the instance metadata endpoint (`/metadata/instance`). The Google auth library calls this automatically.

---

## Usage on the Azure VM

### Environment variable

```bash
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/gcp-creds.json
```

### Python (google-cloud-secret-manager)

```python
from google.cloud import secretmanager

client = secretmanager.SecretManagerServiceClient()
response = client.access_secret_version(
    name="projects/<GCP_PROJECT_ID>/secrets/<SECRET_NAME>/versions/latest"
)
print(response.payload.data.decode("utf-8"))
```

Requires `google-auth >= 1.27.0`.

### gcloud CLI

```bash
gcloud auth login --cred-file=gcp-creds.json
gcloud secrets versions access latest --secret=<SECRET_NAME>
```

### Go

```go
import secretmanager "cloud.google.com/go/secretmanager/apiv1"
// Just set GOOGLE_APPLICATION_CREDENTIALS — the client picks it up automatically.
```

---

## IMDS gotchas

- The IMDS token endpoint is `http://169.254.169.254/metadata/identity/oauth2/token` — HTTP only, not HTTPS.
- Requests **must** include the `Metadata: true` header.
- When curling manually, **quote the URL** — the `&` in `?api-version=...&resource=...` will be interpreted as shell background otherwise:
  ```bash
  curl -s -H Metadata:true --noproxy "*" \
    "http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&resource=api://<CLIENT_ID>"
  ```
- IMDS is rate-limited to ~5 req/s. The Google auth library caches tokens so this is rarely an issue.
- IMDS is unauthenticated — any process on the VM can request tokens. Firewall it if needed.

---

## Variable reference

| Variable                     | Where to find it                                                | Example              |
| ---------------------------- | --------------------------------------------------------------- | -------------------- |
| `AZURE_TENANT_ID`            | Entra ID → Overview → Tenant ID                                 | `72f988bf-...`       |
| `APPLICATION_CLIENT_ID`      | App Registration → Overview                                     | `8d964ed5-...`       |
| `APPLICATION_ID_URI`         | App Registration → Expose an API                                | `api://8d964ed5-...` |
| `MANAGED_IDENTITY_OBJECT_ID` | Managed Identity → Overview → Object (principal) ID             | `a1b2c3d4-...`       |
| `GCP_PROJECT_ID`             | GCP Console → Dashboard                                         | `my-project`         |
| `GCP_PROJECT_NUMBER`         | `gcloud projects describe <ID> --format="value(projectNumber)"` | `123456789012`       |
| `POOL_ID`                    | You choose when creating the pool                               | `azure-pool`         |
| `PROVIDER_ID`                | You choose when creating the provider                           | `azure-provider`     |
| `SA_NAME`                    | You choose when creating the service account                    | `azure-wif-sa`       |
