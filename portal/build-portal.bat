@echo off
echo ============================================
echo  Building portal.exe
echo ============================================
echo.

where dotnet >nul 2>nul
if %ERRORLEVEL% neq 0 (
    echo ERROR: .NET SDK not found. Install from https://dotnet.microsoft.com/download
    exit /b 1
)

REM Build from the src/ subfolder, output to parent of this script (bin/) when deployed
cd /d "%~dp0src"
dotnet publish -c Release -r win-x64 --self-contained -p:PublishSingleFile=true -o "%~dp0.."
if %ERRORLEVEL% neq 0 (
    echo BUILD FAILED
    exit /b 1
)
echo.
echo BUILD SUCCEEDED: portal.exe
