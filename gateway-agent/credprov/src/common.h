#pragma once

#include <initguid.h>
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <credentialprovider.h>
#include <wincred.h>
#include <winhttp.h>
#include <ntsecapi.h>
#include <strsafe.h>
#include <new>

// {E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}
DEFINE_GUID(CLSID_PinCredentialProvider,
    0xe4a3c2b1, 0x7d6f, 0x4a8e, 0x9c, 0x5b, 0x1d, 0x2e, 0x3f, 0x4a, 0x5b, 0x6c);

// UI field identifiers.
enum PIN_FIELD_ID {
    PFI_TITLE  = 0,  // Large text:    "P0rtal Gateway"
    PFI_PIN    = 1,  // Password text: PIN input
    PFI_SUBMIT = 2,  // Submit button
    PFI_STATUS = 3,  // Small text:    error messages
    PFI_COUNT  = 4,
};

// Descriptor for each field shown in the credential tile.
static const CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR s_rgFieldDescriptors[PFI_COUNT] = {
    { PFI_TITLE,  CPFT_LARGE_TEXT,    L"P0rtal Gateway" },
    { PFI_PIN,    CPFT_PASSWORD_TEXT,  L"PIN" },
    { PFI_SUBMIT, CPFT_SUBMIT_BUTTON, L"Connect" },
    { PFI_STATUS, CPFT_SMALL_TEXT,    L"" },
};

// Field display states — which fields are visible and which has focus.
struct FIELD_STATE_PAIR {
    CREDENTIAL_PROVIDER_FIELD_STATE        cpfs;
    CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE cpfis;
};

static const FIELD_STATE_PAIR s_rgFieldStates[PFI_COUNT] = {
    { CPFS_DISPLAY_IN_SELECTED_TILE, CPFIS_NONE },     // title
    { CPFS_DISPLAY_IN_SELECTED_TILE, CPFIS_FOCUSED },  // pin (auto-focused)
    { CPFS_DISPLAY_IN_SELECTED_TILE, CPFIS_NONE },     // submit
    { CPFS_HIDDEN,                   CPFIS_NONE },     // status (shown on error)
};

// Duplicate a wide string using CoTaskMemAlloc (caller frees with CoTaskMemFree).
inline HRESULT CoAllocString(LPCWSTR src, LPWSTR* dest) {
    if (!src || !dest) return E_INVALIDARG;
    size_t cb = (wcslen(src) + 1) * sizeof(WCHAR);
    *dest = static_cast<LPWSTR>(CoTaskMemAlloc(cb));
    if (!*dest) return E_OUTOFMEMORY;
    memcpy(*dest, src, cb);
    return S_OK;
}
