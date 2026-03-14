#pragma once
#include "common.h"

class CPinCredential;

// CPinProvider — the credential provider that LogonUI loads.
// It advertises a single credential tile (CPinCredential) with a PIN field.
class CPinProvider : public ICredentialProvider {
public:
    CPinProvider();

    // IUnknown
    IFACEMETHODIMP_(ULONG) AddRef() override;
    IFACEMETHODIMP_(ULONG) Release() override;
    IFACEMETHODIMP QueryInterface(REFIID riid, void** ppv) override;

    // ICredentialProvider
    IFACEMETHODIMP SetUsageScenario(CREDENTIAL_PROVIDER_USAGE_SCENARIO cpus, DWORD dwFlags) override;
    IFACEMETHODIMP SetSerialization(const CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION* pcpcs) override;
    IFACEMETHODIMP Advise(ICredentialProviderEvents* pcpe, UINT_PTR upAdviseContext) override;
    IFACEMETHODIMP UnAdvise() override;
    IFACEMETHODIMP GetFieldDescriptorCount(DWORD* pdwCount) override;
    IFACEMETHODIMP GetFieldDescriptorAt(DWORD dwIndex, CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR** ppcpfd) override;
    IFACEMETHODIMP GetCredentialCount(DWORD* pdwCount, DWORD* pdwDefault, BOOL* pbAutoLogonCredential) override;
    IFACEMETHODIMP GetCredentialAt(DWORD dwIndex, ICredentialProviderCredential** ppcpc) override;

private:
    ~CPinProvider();
    LONG            _cRef;
    CPinCredential* _pCredential;
};
