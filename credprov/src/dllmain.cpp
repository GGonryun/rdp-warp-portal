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

// Self-registration — called by regsvr32 to write COM + credential provider
// registry entries so LogonUI can discover and load the DLL.
STDAPI DllRegisterServer() {
    WCHAR dllPath[MAX_PATH];
    GetModuleFileNameW(g_hModule, dllPath, MAX_PATH);

    // Register COM CLSID
    HKEY hKey = NULL;
    LSTATUS ls = RegCreateKeyExW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Classes\\CLSID\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}",
        0, NULL, 0, KEY_WRITE, NULL, &hKey, NULL);
    if (ls != ERROR_SUCCESS) return HRESULT_FROM_WIN32(ls);
    RegSetValueExW(hKey, NULL, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(L"P0rtal PIN Credential Provider"),
        sizeof(L"P0rtal PIN Credential Provider"));
    RegCloseKey(hKey);

    // InprocServer32
    ls = RegCreateKeyExW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Classes\\CLSID\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}\\InprocServer32",
        0, NULL, 0, KEY_WRITE, NULL, &hKey, NULL);
    if (ls != ERROR_SUCCESS) return HRESULT_FROM_WIN32(ls);
    RegSetValueExW(hKey, NULL, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(dllPath),
        static_cast<DWORD>((wcslen(dllPath) + 1) * sizeof(WCHAR)));
    LPCWSTR threadModel = L"Apartment";
    RegSetValueExW(hKey, L"ThreadingModel", 0, REG_SZ,
        reinterpret_cast<const BYTE*>(threadModel),
        static_cast<DWORD>((wcslen(threadModel) + 1) * sizeof(WCHAR)));
    RegCloseKey(hKey);

    // Register as a credential provider
    ls = RegCreateKeyExW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\Credential Providers\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}",
        0, NULL, 0, KEY_WRITE, NULL, &hKey, NULL);
    if (ls != ERROR_SUCCESS) return HRESULT_FROM_WIN32(ls);
    RegSetValueExW(hKey, NULL, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(L"P0rtal PIN Credential Provider"),
        sizeof(L"P0rtal PIN Credential Provider"));
    RegCloseKey(hKey);

    return S_OK;
}

STDAPI DllUnregisterServer() {
    RegDeleteTreeW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Classes\\CLSID\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}");
    RegDeleteTreeW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\Credential Providers\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}");
    return S_OK;
}
