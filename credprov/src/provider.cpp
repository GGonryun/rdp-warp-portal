#include "provider.h"
#include "credential.h"

CPinProvider::CPinProvider() :
    _cRef(1),
    _pCredential(nullptr) {}

CPinProvider::~CPinProvider() {
    if (_pCredential) {
        _pCredential->Release();
    }
}

// IUnknown -------------------------------------------------------------------

HRESULT CPinProvider::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_INVALIDARG;
    *ppv = nullptr;
    if (riid == IID_IUnknown || riid == IID_ICredentialProvider) {
        *ppv = static_cast<ICredentialProvider*>(this);
        AddRef();
        return S_OK;
    }
    return E_NOINTERFACE;
}

ULONG CPinProvider::AddRef()  { return InterlockedIncrement(&_cRef); }
ULONG CPinProvider::Release() {
    LONG c = InterlockedDecrement(&_cRef);
    if (c == 0) delete this;
    return c;
}

// ICredentialProvider --------------------------------------------------------

HRESULT CPinProvider::SetUsageScenario(
    CREDENTIAL_PROVIDER_USAGE_SCENARIO cpus, DWORD /*dwFlags*/)
{
    switch (cpus) {
    case CPUS_LOGON:
    case CPUS_UNLOCK_WORKSTATION:
    case CPUS_CREDUI:
        _pCredential = new(std::nothrow) CPinCredential();
        return _pCredential ? S_OK : E_OUTOFMEMORY;
    default:
        return E_NOTIMPL;
    }
}

HRESULT CPinProvider::SetSerialization(
    const CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION* /*pcpcs*/)
{
    return E_NOTIMPL;
}

HRESULT CPinProvider::Advise(ICredentialProviderEvents*, UINT_PTR) { return S_OK; }
HRESULT CPinProvider::UnAdvise() { return S_OK; }

HRESULT CPinProvider::GetFieldDescriptorCount(DWORD* pdwCount) {
    *pdwCount = PFI_COUNT;
    return S_OK;
}

HRESULT CPinProvider::GetFieldDescriptorAt(
    DWORD dwIndex, CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR** ppcpfd)
{
    if (dwIndex >= PFI_COUNT || !ppcpfd) return E_INVALIDARG;

    // Allocate a CoTaskMem copy — LogonUI owns it and calls CoTaskMemFree.
    auto* pfd = static_cast<CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR*>(
        CoTaskMemAlloc(sizeof(CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR)));
    if (!pfd) return E_OUTOFMEMORY;

    *pfd = s_rgFieldDescriptors[dwIndex];
    pfd->pszLabel = nullptr;
    if (s_rgFieldDescriptors[dwIndex].pszLabel) {
        CoAllocString(s_rgFieldDescriptors[dwIndex].pszLabel, &pfd->pszLabel);
    }

    *ppcpfd = pfd;
    return S_OK;
}

HRESULT CPinProvider::GetCredentialCount(
    DWORD* pdwCount, DWORD* pdwDefault, BOOL* pbAutoLogonCredential)
{
    *pdwCount = 1;
    *pdwDefault = 0;
    *pbAutoLogonCredential = FALSE;
    return S_OK;
}

HRESULT CPinProvider::GetCredentialAt(
    DWORD dwIndex, ICredentialProviderCredential** ppcpc)
{
    if (dwIndex != 0 || !ppcpc) return E_INVALIDARG;
    _pCredential->AddRef();
    *ppcpc = _pCredential;
    return S_OK;
}
