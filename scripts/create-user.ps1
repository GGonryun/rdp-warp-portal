param(
    [Parameter(Mandatory)]
    [string]$Username,

    [Parameter(Mandatory)]
    [string]$Password,

    [string]$FullName = $Username
)

$SecurePassword = ConvertTo-SecureString $Password -AsPlainText -Force

New-LocalUser -Name $Username -Password $SecurePassword -FullName $FullName -Description "Administrator account" -PasswordNeverExpires

Add-LocalGroupMember -Group "Administrators" -Member $Username

Write-Host "User '$Username' created and added to Administrators group." -ForegroundColor Green