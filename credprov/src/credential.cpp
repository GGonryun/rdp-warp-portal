#include "credential.h"

CPinCredential::CPinCredential() :
    _cRef(1),
    _pEvents(nullptr)
{
    _wszPin[0] = L'\0';
}

CPinCredential::~CPinCredential() {
    SecureZeroMemory(_wszPin, sizeof(_wszPin));
    if (_pEvents) _pEvents->Release();
}

// IUnknown -------------------------------------------------------------------

HRESULT CPinCredential::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_INVALIDARG;
    *ppv = nullptr;
    if (riid == IID_IUnknown || riid == IID_ICredentialProviderCredential) {
        *ppv = static_cast<ICredentialProviderCredential*>(this);
        AddRef();
        return S_OK;
    }
    return E_NOINTERFACE;
}

ULONG CPinCredential::AddRef()  { return InterlockedIncrement(&_cRef); }
ULONG CPinCredential::Release() {
    LONG c = InterlockedDecrement(&_cRef);
    if (c == 0) delete this;
    return c;
}

// ICredentialProviderCredential — lifecycle -----------------------------------

HRESULT CPinCredential::Advise(ICredentialProviderCredentialEvents* pcpce) {
    if (_pEvents) _pEvents->Release();
    _pEvents = pcpce;
    if (_pEvents) _pEvents->AddRef();
    return S_OK;
}

HRESULT CPinCredential::UnAdvise() {
    if (_pEvents) { _pEvents->Release(); _pEvents = nullptr; }
    return S_OK;
}

HRESULT CPinCredential::SetSelected(BOOL* pbAutoLogon) {
    *pbAutoLogon = FALSE;
    return S_OK;
}

HRESULT CPinCredential::SetDeselected() {
    SecureZeroMemory(_wszPin, sizeof(_wszPin));
    if (_pEvents) {
        _pEvents->SetFieldString(this, PFI_PIN, L"");
    }
    return S_OK;
}

// ICredentialProviderCredential — field accessors ----------------------------

HRESULT CPinCredential::GetFieldState(DWORD dwFieldID,
    CREDENTIAL_PROVIDER_FIELD_STATE* pcpfs,
    CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE* pcpfis)
{
    if (dwFieldID >= PFI_COUNT) return E_INVALIDARG;
    *pcpfs  = s_rgFieldStates[dwFieldID].cpfs;
    *pcpfis = s_rgFieldStates[dwFieldID].cpfis;
    return S_OK;
}

HRESULT CPinCredential::GetStringValue(DWORD dwFieldID, PWSTR* ppwsz) {
    switch (dwFieldID) {
    case PFI_TITLE:  return CoAllocString(L"P0rtal Gateway", ppwsz);
    case PFI_PIN:    return CoAllocString(L"", ppwsz);
    case PFI_STATUS: return CoAllocString(L"", ppwsz);
    default:         return E_INVALIDARG;
    }
}

HRESULT CPinCredential::GetBitmapValue(DWORD, HBITMAP*)                    { return E_NOTIMPL; }
HRESULT CPinCredential::GetCheckboxValue(DWORD, BOOL*, PWSTR*)            { return E_NOTIMPL; }
HRESULT CPinCredential::GetComboBoxValueCount(DWORD, DWORD*, DWORD*)      { return E_NOTIMPL; }
HRESULT CPinCredential::GetComboBoxValueAt(DWORD, DWORD, PWSTR*)          { return E_NOTIMPL; }
HRESULT CPinCredential::SetCheckboxValue(DWORD, BOOL)                     { return E_NOTIMPL; }
HRESULT CPinCredential::SetComboBoxSelectedValue(DWORD, DWORD)            { return E_NOTIMPL; }
HRESULT CPinCredential::CommandLinkClicked(DWORD)                         { return E_NOTIMPL; }

HRESULT CPinCredential::GetSubmitButtonValue(DWORD dwFieldID, DWORD* pdwAdjacentTo) {
    if (dwFieldID != PFI_SUBMIT) return E_INVALIDARG;
    *pdwAdjacentTo = PFI_PIN;  // submit button sits next to the PIN field
    return S_OK;
}

HRESULT CPinCredential::SetStringValue(DWORD dwFieldID, PCWSTR pwz) {
    if (dwFieldID == PFI_PIN) {
        StringCchCopyW(_wszPin, ARRAYSIZE(_wszPin), pwz);
        return S_OK;
    }
    return E_INVALIDARG;
}

// ---------------------------------------------------------------------------
// ResolvePinToUsername — POST the PIN to the gateway agent's local API and
// receive the matching gateway session username.
//
//   POST http://localhost:8080/internal/auth/resolve-pin
//   Body: {"pin":"123456"}
//   200:  {"username":"gwsession003"}
//   401:  {"error":"invalid pin"}
// ---------------------------------------------------------------------------
bool CPinCredential::ResolvePinToUsername(
    LPCWSTR pin, WCHAR* username, DWORD cchUsername)
{
    bool ok = false;
    HINTERNET hSession = NULL, hConnect = NULL, hRequest = NULL;

    hSession = WinHttpOpen(L"PinCredentialProvider/1.0",
        WINHTTP_ACCESS_TYPE_NO_PROXY, NULL, NULL, 0);
    if (!hSession) goto done;

    hConnect = WinHttpConnect(hSession, L"localhost", 8080, 0);
    if (!hConnect) goto done;

    hRequest = WinHttpOpenRequest(hConnect, L"POST",
        L"/internal/auth/resolve-pin",
        NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, 0);
    if (!hRequest) goto done;

    {
        // Convert PIN to UTF-8 and build the JSON body.
        char pinUtf8[32] = {};
        WideCharToMultiByte(CP_UTF8, 0, pin, -1,
            pinUtf8, sizeof(pinUtf8), NULL, NULL);

        char jsonBody[128] = {};
        StringCbPrintfA(jsonBody, sizeof(jsonBody),
            "{\"pin\":\"%s\"}", pinUtf8);
        DWORD bodyLen = static_cast<DWORD>(strlen(jsonBody));

        LPCWSTR hdrs = L"Content-Type: application/json\r\n";
        if (!WinHttpSendRequest(hRequest, hdrs, (DWORD)-1L,
                jsonBody, bodyLen, bodyLen, 0))
            goto done;

        if (!WinHttpReceiveResponse(hRequest, NULL))
            goto done;

        DWORD statusCode = 0, statusSize = sizeof(statusCode);
        WinHttpQueryHeaders(hRequest,
            WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER,
            NULL, &statusCode, &statusSize, NULL);
        if (statusCode != 200) goto done;

        char resp[512] = {};
        DWORD bytesRead = 0;
        if (!WinHttpReadData(hRequest, resp, sizeof(resp) - 1, &bytesRead))
            goto done;
        resp[bytesRead] = '\0';

        // Minimal JSON parse — find "username":"<value>".
        char* p = strstr(resp, "\"username\":\"");
        if (!p) goto done;
        p += 12; // skip past "username":"
        char* end = strchr(p, '"');
        if (!end) goto done;
        *end = '\0';

        MultiByteToWideChar(CP_UTF8, 0, p, -1, username, cchUsername);
        ok = true;
    }

done:
    if (hRequest) WinHttpCloseHandle(hRequest);
    if (hConnect) WinHttpCloseHandle(hConnect);
    if (hSession) WinHttpCloseHandle(hSession);
    return ok;
}

// ---------------------------------------------------------------------------
// GetSerialization — called when the user submits the PIN.
// Resolves PIN → username via the agent API, then packages username + PIN
// into a credential buffer for Windows authentication.
// ---------------------------------------------------------------------------
HRESULT CPinCredential::GetSerialization(
    CREDENTIAL_PROVIDER_GET_SERIALIZATION_RESPONSE* pcpgsr,
    CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION*   pcpcs,
    PWSTR*                                          ppwszOptionalStatusText,
    CREDENTIAL_PROVIDER_STATUS_ICON*                pcpsiOptionalStatusIcon)
{
    *pcpgsr = CPGSR_NO_CREDENTIAL_NOT_FINISHED;

    if (_wszPin[0] == L'\0') {
        CoAllocString(L"Please enter your PIN", ppwszOptionalStatusText);
        *pcpsiOptionalStatusIcon = CPSI_ERROR;
        return S_OK;
    }

    // Resolve PIN to a gateway session username.
    WCHAR username[64] = {};
    if (!ResolvePinToUsername(_wszPin, username, ARRAYSIZE(username))) {
        CoAllocString(L"Invalid PIN", ppwszOptionalStatusText);
        *pcpsiOptionalStatusIcon = CPSI_ERROR;

        if (_pEvents) {
            _pEvents->SetFieldState(this, PFI_STATUS, CPFS_DISPLAY_IN_SELECTED_TILE);
            _pEvents->SetFieldString(this, PFI_STATUS, L"Invalid PIN. Try again.");
        }
        return S_OK;
    }

    // Prepend ".\" for local account logon.
    WCHAR qualifiedUser[128] = {};
    StringCchPrintfW(qualifiedUser, ARRAYSIZE(qualifiedUser), L".\\%s", username);

    // Pack credentials into a serialization buffer.
    DWORD cbBuf = 0;
    CredPackAuthenticationBufferW(0, qualifiedUser, _wszPin, NULL, &cbBuf);

    BYTE* pbBuf = static_cast<BYTE*>(CoTaskMemAlloc(cbBuf));
    if (!pbBuf) return E_OUTOFMEMORY;

    if (!CredPackAuthenticationBufferW(0, qualifiedUser, _wszPin, pbBuf, &cbBuf)) {
        CoTaskMemFree(pbBuf);
        return HRESULT_FROM_WIN32(GetLastError());
    }

    // Look up the Negotiate authentication package.
    ULONG authPackage = 0;
    {
        HANDLE hLsa = NULL;
        if (SUCCEEDED(HRESULT_FROM_NT(LsaConnectUntrusted(&hLsa)))) {
            LSA_STRING lsaName;
            lsaName.Buffer        = const_cast<PCHAR>("Negotiate");
            lsaName.Length        = 9;
            lsaName.MaximumLength = 10;
            LsaLookupAuthenticationPackage(hLsa, &lsaName, &authPackage);
            LsaDeregisterLogonProcess(hLsa);
        }
    }

    pcpcs->clsidCredentialProvider = CLSID_PinCredentialProvider;
    pcpcs->rgbSerialization        = pbBuf;
    pcpcs->cbSerialization         = cbBuf;
    pcpcs->ulAuthenticationPackage = authPackage;

    *pcpgsr = CPGSR_RETURN_CREDENTIAL_FINISHED;

    SecureZeroMemory(_wszPin, sizeof(_wszPin));
    return S_OK;
}

// ---------------------------------------------------------------------------
// ReportResult — called after Windows processes the credential.
// On failure, show a friendly error message.
// ---------------------------------------------------------------------------
HRESULT CPinCredential::ReportResult(
    NTSTATUS ntsStatus, NTSTATUS /*ntsSubstatus*/,
    PWSTR* ppwszOptionalStatusText,
    CREDENTIAL_PROVIDER_STATUS_ICON* pcpsiOptionalStatusIcon)
{
    if (ntsStatus != 0) {
        CoAllocString(L"Invalid PIN. Please try again.", ppwszOptionalStatusText);
        *pcpsiOptionalStatusIcon = CPSI_ERROR;
    }
    return S_OK;
}
