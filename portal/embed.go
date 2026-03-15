package portal

import "embed"

//go:embed src/*.cs src/*.csproj build-portal.bat
var Source embed.FS
