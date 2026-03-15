#pragma once
#include "common.h"

// CPinCredential — the single credential tile shown by CPinProvider.
// It presents a PIN input field, resolves the PIN to a gateway username via
// the local agent API, and packages the credentials for Windows logon.
class CPinCredential : public ICredentialProviderCredential {
public:
    CPinCredential();

    // IUnknown
    IFACEMETHODIMP_(ULONG) AddRef() override;
    IFACEMETHODIMP_(ULONG) Release() override;
    IFACEMETHODIMP QueryInterface(REFIID riid, void** ppv) override;

    // ICredentialProviderCredential
    IFACEMETHODIMP Advise(ICredentialProviderCredentialEvents* pcpce) override;
    IFACEMETHODIMP UnAdvise() override;
    IFACEMETHODIMP SetSelected(BOOL* pbAutoLogon) override;
    IFACEMETHODIMP SetDeselected() override;
    IFACEMETHODIMP GetFieldState(DWORD dwFieldID,
        CREDENTIAL_PROVIDER_FIELD_STATE* pcpfs,
        CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE* pcpfis) override;
    IFACEMETHODIMP GetStringValue(DWORD dwFieldID, PWSTR* ppwsz) override;
    IFACEMETHODIMP GetBitmapValue(DWORD dwFieldID, HBITMAP* phbmp) override;
    IFACEMETHODIMP GetCheckboxValue(DWORD dwFieldID, BOOL* pbChecked, PWSTR* ppwszLabel) override;
    IFACEMETHODIMP GetComboBoxValueCount(DWORD dwFieldID, DWORD* pcItems, DWORD* pdwSelectedItem) override;
    IFACEMETHODIMP GetComboBoxValueAt(DWORD dwFieldID, DWORD dwItem, PWSTR* ppwszItem) override;
    IFACEMETHODIMP GetSubmitButtonValue(DWORD dwFieldID, DWORD* pdwAdjacentTo) override;
    IFACEMETHODIMP SetStringValue(DWORD dwFieldID, PCWSTR pwz) override;
    IFACEMETHODIMP SetCheckboxValue(DWORD dwFieldID, BOOL bChecked) override;
    IFACEMETHODIMP SetComboBoxSelectedValue(DWORD dwFieldID, DWORD dwSelectedItem) override;
    IFACEMETHODIMP CommandLinkClicked(DWORD dwFieldID) override;
    IFACEMETHODIMP GetSerialization(
        CREDENTIAL_PROVIDER_GET_SERIALIZATION_RESPONSE* pcpgsr,
        CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION* pcpcs,
        PWSTR* ppwszOptionalStatusText,
        CREDENTIAL_PROVIDER_STATUS_ICON* pcpsiOptionalStatusIcon) override;
    IFACEMETHODIMP ReportResult(NTSTATUS ntsStatus, NTSTATUS ntsSubstatus,
        PWSTR* ppwszOptionalStatusText,
        CREDENTIAL_PROVIDER_STATUS_ICON* pcpsiOptionalStatusIcon) override;

private:
    ~CPinCredential();
    bool ResolvePinToUsername(LPCWSTR pin, WCHAR* username, DWORD cchUsername);

    LONG                                  _cRef;
    ICredentialProviderCredentialEvents*   _pEvents;
    WCHAR                                 _wszPin[16];
};
