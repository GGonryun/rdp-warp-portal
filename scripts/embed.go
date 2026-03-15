package scripts

import _ "embed"

//go:embed install-bastion.ps1
var InstallScript []byte

//go:embed session-launch.ps1
var SessionLaunchScript []byte
