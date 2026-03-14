#include "provider.h"

static LONG    g_cRef    = 0;
static HMODULE g_hModule = NULL;

// Class factory — creates CPinProvider instances for COM.
class CPinProviderFactory : public IClassFactory {
public:
    // IUnknown (singleton — prevent destroy)
    IFACEMETHODIMP_(ULONG) AddRef()  override { return 2; }
    IFACEMETHODIMP_(ULONG) Release() override { return 1; }

    IFACEMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_IClassFactory) {
            *ppv = static_cast<IClassFactory*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }

    // IClassFactory
    IFACEMETHODIMP CreateInstance(IUnknown* pUnkOuter, REFIID riid, void** ppv) override {
        if (pUnkOuter) return CLASS_E_NOAGGREGATION;
        CPinProvider* p = new(std::nothrow) CPinProvider();
        if (!p) return E_OUTOFMEMORY;
        HRESULT hr = p->QueryInterface(riid, ppv);
        p->Release();
        return hr;
    }

    IFACEMETHODIMP LockServer(BOOL bLock) override {
        if (bLock) InterlockedIncrement(&g_cRef);
        else       InterlockedDecrement(&g_cRef);
        return S_OK;
    }
};

static CPinProviderFactory g_factory;

BOOL APIENTRY DllMain(HMODULE hModule, DWORD dwReason, LPVOID) {
    if (dwReason == DLL_PROCESS_ATTACH) {
        g_hModule = hModule;
        DisableThreadLibraryCalls(hModule);
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (rclsid == CLSID_PinCredentialProvider) {
        return g_factory.QueryInterface(riid, ppv);
    }
    *ppv = nullptr;
    return CLASS_E_CLASSNOTAVAILABLE;
}

STDAPI DllCanUnloadNow() {
    return g_cRef == 0 ? S_OK : S_FALSE;
}
