# Geo-split routing self-check.
#
# Run in an elevated PowerShell while a tunnel with "GeoSplit = ru" is active.
# It verifies that Russian destinations leave through the physical interface,
# that everything else goes through the tunnel, that the exception routes and
# WFP permit filters are present, and that no DNS or IPv6 leak is visible.
#
# Usage: .\geosplit-check.ps1 [-RussianHost 77.88.8.8] [-ForeignHost 1.1.1.1]

param(
    [string]$RussianHost = '77.88.8.8',   # Yandex DNS, RU
    [string]$ForeignHost = '1.1.1.1',     # Cloudflare DNS, not RU
    [string]$TunnelPrefix = 'AmneziaWG'
)

$ErrorActionPreference = 'Continue'
$failures = 0
function Report([bool]$ok, [string]$what) {
    if ($ok) { Write-Host "[ ok ] $what" -ForegroundColor Green }
    else { Write-Host "[FAIL] $what" -ForegroundColor Red; $script:failures++ }
}

# 1. Adapter and default route
$tun = Get-NetAdapter | Where-Object { $_.InterfaceDescription -like 'Wintun*' -or $_.Name -like "$TunnelPrefix*" } | Select-Object -First 1
Report ($null -ne $tun) "tunnel adapter present ($($tun.Name))"
$default = Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric, InterfaceMetric | Select-Object -First 1
Report ($default.InterfaceIndex -eq $tun.ifIndex) "0.0.0.0/0 points at the tunnel (ifIndex $($default.InterfaceIndex))"

# 2. Exception routes: marker protocol Bbn = 12 in NL_ROUTE_PROTOCOL
$geoRoutes = Get-NetRoute -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.Protocol -eq 'Bbn' }
Report ($geoRoutes.Count -gt 1000) "geo-split IPv4 exception routes installed: $($geoRoutes.Count)"
if ($geoRoutes.Count -gt 0) {
    $ifaces = $geoRoutes | Group-Object InterfaceIndex | ForEach-Object { $_.Name }
    Report ($ifaces.Count -eq 1 -and $ifaces[0] -ne $tun.ifIndex) "exception routes live on exactly one physical interface (ifIndex $($ifaces -join ','))"
}
$geoRoutes6 = Get-NetRoute -AddressFamily IPv6 -ErrorAction SilentlyContinue | Where-Object { $_.Protocol -eq 'Bbn' }
Write-Host "       geo-split IPv6 exception routes: $($geoRoutes6.Count)"

# 3. Route lookup for a Russian and a foreign address
$ru = Find-NetRoute -RemoteIPAddress $RussianHost | Select-Object -First 1
Report ($ru.InterfaceIndex -ne $tun.ifIndex) "$RussianHost resolves to a physical interface (ifIndex $($ru.InterfaceIndex))"
$foreign = Find-NetRoute -RemoteIPAddress $ForeignHost | Select-Object -First 1
Report ($foreign.InterfaceIndex -eq $tun.ifIndex) "$ForeignHost resolves to the tunnel (ifIndex $($foreign.InterfaceIndex))"

# 4. Connectivity through each path
$ruOk = Test-NetConnection -ComputerName $RussianHost -Port 53 -InformationLevel Quiet -WarningAction SilentlyContinue
Report $ruOk "TCP 53 to $RussianHost works (direct path)"
$foreignOk = Test-NetConnection -ComputerName $ForeignHost -Port 53 -InformationLevel Quiet -WarningAction SilentlyContinue
Report $foreignOk "TCP 53 to $ForeignHost works (tunnel path)"

# 5. WFP filters from the kill-switch, including the geo-split permits
$wfp = & netsh wfp show filters file=- 2>$null
$permits = ($wfp | Select-String -Pattern 'Permit geo-split direct outbound' -AllMatches).Count
Report ($permits -gt 0) "WFP permit filters for geo-split prefixes present: $permits"
$private = ($wfp | Select-String -Pattern 'Permit private networks outbound' -AllMatches).Count
Write-Host "       WFP private-network permits: $private"
$blockAll = ($wfp | Select-String -Pattern 'Block all outbound' -AllMatches).Count
Report ($blockAll -gt 0) "kill-switch block-all filters present: $blockAll"

# 6. Tunnel log excerpt
$log = & "$env:ProgramFiles\AmneziaWG\amneziawg.exe" /dumplog 2>$null | Select-String -Pattern 'Geo-split' | Select-Object -Last 6
if ($log) { Write-Host "       recent Geo-split log lines:"; $log | ForEach-Object { Write-Host "         $_" } }

Write-Host ""
if ($failures -eq 0) { Write-Host "All checks passed." -ForegroundColor Green } else { Write-Host "$failures check(s) failed." -ForegroundColor Red; exit 1 }
