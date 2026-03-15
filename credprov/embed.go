package credprov

import "embed"

//go:embed src/*.h src/*.cpp build.bat PinCredentialProvider.def
var Source embed.FS
