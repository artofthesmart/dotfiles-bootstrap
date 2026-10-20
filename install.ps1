[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = if ($env:LOCAL_AI_REPOSITORY) { $env:LOCAL_AI_REPOSITORY } else { 'artofthesmart/dotfiles-bootstrap' }
$api = if ($env:LOCAL_AI_GITHUB_API_BASE) { $env:LOCAL_AI_GITHUB_API_BASE.TrimEnd('/') } else { 'https://api.github.com' }
if (-not $api.StartsWith('https://', [StringComparison]::OrdinalIgnoreCase)) { throw 'Release API must use HTTPS.' }
$arch = switch ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    'X64' { 'amd64' }
    'Arm64' { 'arm64' }
    default { throw 'Unsupported Windows architecture.' }
}
$root = Join-Path $env:LOCALAPPDATA 'LocalAI\Cache'
$work = Join-Path $root ("bootstrap." + [Diagnostics.Process]::GetCurrentProcess().Id)
New-Item -ItemType Directory -Force -Path $work | Out-Null

function Get-BoundedFile([string]$Uri, [string]$OutFile, [long]$MaximumBytes) {
    if (-not $Uri.StartsWith('https://', [StringComparison]::OrdinalIgnoreCase)) { throw 'Download URL must use HTTPS.' }
    Invoke-WebRequest -UseBasicParsing -MaximumRedirection 10 -TimeoutSec 180 -Uri $Uri -OutFile $OutFile
    $size = (Get-Item -LiteralPath $OutFile).Length
    if ($size -le 0 -or $size -gt $MaximumBytes) { throw 'Download is empty or exceeds its size bound.' }
}

try {
    $releaseFile = Join-Path $work 'release.json'
    Get-BoundedFile "$api/repos/$repo/releases/latest" $releaseFile 1MB
    $release = Get-Content -Raw -LiteralPath $releaseFile | ConvertFrom-Json
    if ($release.draft -or $release.prerelease -or -not $release.tag_name) { throw 'Latest release is not stable.' }
    $version = [string]$release.tag_name -replace '^v', ''
    $names = @("local-ai_windows_${arch}.zip", "local-ai_${version}_windows_${arch}.zip")
    $matches = @($release.assets | Where-Object { $names -contains $_.name })
    $sumsAsset = @($release.assets | Where-Object { $_.name -eq 'SHA256SUMS' })
    if ($matches.Count -ne 1 -or $sumsAsset.Count -ne 1) { throw 'Compatible release asset or SHA256SUMS is missing.' }
    $asset = $matches[0]
    $archive = Join-Path $work $asset.name
    $sums = Join-Path $work 'SHA256SUMS'
    Get-BoundedFile $sumsAsset[0].browser_download_url $sums 4MB
    Get-BoundedFile $asset.browser_download_url $archive 256MB
    $escaped = [Regex]::Escape([string]$asset.name)
    $entries = @(Get-Content -LiteralPath $sums | ForEach-Object {
        if ($_ -match "^([0-9A-Fa-f]{64})\s+\*?$escaped$") { $Matches[1].ToLowerInvariant() }
    })
    if ($entries.Count -ne 1) { throw 'Checksum entry is missing or ambiguous.' }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
    if ($actual -ne $entries[0]) { throw 'SHA-256 verification failed.' }

    if ($env:LOCAL_AI_VERIFY_ATTESTATION -eq '1') {
        $gh = Get-Command gh -ErrorAction SilentlyContinue
        if ($gh) {
            & $gh.Source attestation verify $archive --repo $repo
            if ($LASTEXITCODE -ne 0) { throw 'Optional GitHub attestation verification failed.' }
        } else {
            Write-Warning 'gh is not installed; optional attestation verification skipped.'
        }
    }

    $binDir = Join-Path $env:LOCALAPPDATA 'Programs\LocalAI'
    $target = Join-Path $binDir 'local-ai.exe'
    $stage = Join-Path $binDir ('.local-ai.new.' + [Diagnostics.Process]::GetCurrentProcess().Id + '.exe')
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    if (Test-Path -LiteralPath $target) {
        & $target update
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        & $target setup @args
        exit $LASTEXITCODE
    }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($archive)
    try {
        $found = @()
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\','/')
            if ([IO.Path]::IsPathRooted($name) -or $name.Split('/') -contains '..' -or $name.Contains(':')) { throw 'Unsafe archive path.' }
            $unixMode = ($entry.ExternalAttributes -shr 16) -band 0xF000
            if ($entry.Name -and $unixMode -ne 0 -and $unixMode -ne 0x8000) { throw 'Unsafe archive entry type.' }
            if ($entry.Name -eq 'local-ai.exe') { $found += $entry }
        }
        if ($found.Count -ne 1 -or $found[0].Length -le 0 -or $found[0].Length -gt 128MB) { throw 'Manager executable is missing, duplicated, or invalid.' }
        [IO.Compression.ZipFileExtensions]::ExtractToFile($found[0], $stage, $false)
    } finally { $zip.Dispose() }
    & $stage version | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Downloaded manager failed its self-check.' }
    Move-Item -Force -LiteralPath $stage -Destination $target
    & $target version | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Installed manager failed its self-check.' }

    $profilePath = $PROFILE.CurrentUserAllHosts
    $marker = @'
# >>> local-ai managed PATH >>>
if (($env:Path -split [IO.Path]::PathSeparator) -notcontains "$env:LOCALAPPDATA\Programs\LocalAI") { $env:Path = "$env:LOCALAPPDATA\Programs\LocalAI" + [IO.Path]::PathSeparator + $env:Path }
# <<< local-ai managed PATH <<<
'@
    if (($env:Path -split [IO.Path]::PathSeparator) -notcontains $binDir) {
        Write-Host "local-ai can add this marker-owned PATH block to $profilePath`n$marker"
        $answer = Read-Host 'Add it? [y/N]'
        if ($answer -match '^(?i:y|yes)$') {
            $existing = if (Test-Path -LiteralPath $profilePath) { Get-Content -Raw -LiteralPath $profilePath } else { '' }
            if ($existing.Contains('# >>> local-ai managed PATH >>>')) {
                Write-Warning 'Existing PATH marker left unchanged.'
            } else {
                $backup = "$profilePath.local-ai.backup"
                if (Test-Path -LiteralPath $backup) { throw 'Refusing to overwrite PATH backup.' }
                New-Item -ItemType Directory -Force -Path (Split-Path -Parent $profilePath) | Out-Null
                if ($existing) { Copy-Item -LiteralPath $profilePath -Destination $backup }
                [IO.File]::WriteAllText("$profilePath.local-ai.new", $existing + [Environment]::NewLine + $marker, [Text.UTF8Encoding]::new($false))
                Move-Item -Force -LiteralPath "$profilePath.local-ai.new" -Destination $profilePath
            }
        } else { Write-Host "PATH unchanged; invoke $target directly." }
    }
    Write-Warning 'This friend-preview binary is unsigned. Do not disable SmartScreen or Defender; confirm provenance and use normal Windows prompts.'
    & $target setup @args
    exit $LASTEXITCODE
} finally {
    Remove-Item -Recurse -Force -LiteralPath $work -ErrorAction SilentlyContinue
}
