@echo off
REM Build the P0rtal PIN Credential Provider DLL.
REM Run from a "Developer Command Prompt for VS 2022" (or any VS version
REM with the Windows SDK installed).
REM
REM Output: PinCredentialProvider.dll

setlocal

cl.exe /nologo /EHsc /W4 /O2 /LD ^
    /DUNICODE /D_UNICODE ^
    src\dllmain.cpp src\provider.cpp src\credential.cpp ^
    /Fe:PinCredentialProvider.dll ^
    /link /DEF:PinCredentialProvider.def ^
    ole32.lib credui.lib winhttp.lib secur32.lib advapi32.lib

if %ERRORLEVEL% neq 0 (
    echo BUILD FAILED
    exit /b 1
)

echo.
echo BUILD SUCCEEDED: PinCredentialProvider.dll
echo.
echo To deploy:
echo   1. Copy PinCredentialProvider.dll to C:\Gateway\bin\
echo   2. Run: regsvr32 /s C:\Gateway\bin\PinCredentialProvider.dll
echo   3. Or use the install-bastion.ps1 script which handles registration.
