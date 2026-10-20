# Native Windows PowerShell 5.1+; no Python or package-manager prerequisite.
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:VerifiedCandidates = $null

function Read-Choice([string]$Message) {
    $answer = Read-Host "$Message (q to quit)"
    if ($answer -ceq 'q') { throw [OperationCanceledException]::new('Setup cancelled.') }
    return $answer
}
function Confirm-Step([string]$Message) {
    return (Read-Choice "$Message [y/N]") -match '^(y|yes)$'
}
function Invoke-NativeOutput([string]$File, [string[]]$Arguments) {
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $output = @(& $File @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousPreference
    }
    if ($exitCode -ne 0) {
        $command = (@($File) + $Arguments) -join ' '
        $detail = @($output | ForEach-Object { [string]$_ }) -join "`n"
        if ($detail) { throw "Command failed (exit $exitCode): $command`n$detail" }
        throw "Command failed (exit $exitCode): $command. Resolve the error and rerun."
    }
    return @($output | ForEach-Object { [string]$_ })
}
function Invoke-Native([string]$File, [string[]]$Arguments) {
    foreach ($line in @(Invoke-NativeOutput $File $Arguments)) { Write-Host $line }
}
function Assert-HttpsResponse($Response) {
    if ($null -eq $Response.ResponseUri -or $Response.ResponseUri.Scheme -ne 'https') {
        throw 'An HTTPS request redirected to a non-HTTPS destination. Refusing the response.'
    }
}
function Get-Application([string]$Name, [string[]]$Candidates) {
    $command = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    foreach ($candidate in $Candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}
function Get-Code {
    return Get-Application 'code.cmd' @(
        "$env:LOCALAPPDATA\Programs\Microsoft VS Code\bin\code.cmd",
        "$env:ProgramFiles\Microsoft VS Code\bin\code.cmd"
    )
}
function Get-Ollama {
    return Get-Application 'ollama.exe' @("$env:LOCALAPPDATA\Programs\Ollama\ollama.exe")
}
function Write-SetupStep([int]$Number, [string]$Title) {
    Write-Host "`n[$Number/4] $Title" -ForegroundColor Cyan
}
function Format-DownloadSize([long]$Bytes) {
    if ($Bytes -ge 1GB) { return ('{0:N1} GB' -f ($Bytes / 1GB)) }
    return ('{0:N1} MB' -f ($Bytes / 1MB))
}
function New-DownloadRequest([uri]$Uri) {
    $request = [Net.WebRequest]::Create($Uri)
    $request.Timeout = 30000
    $request.ReadWriteTimeout = 30000
    return $request
}
function Save-Download([string]$Uri, [string]$Destination, [string]$Name = 'installer') {
    # Windows PowerShell's default protocols may not include TLS 1.2.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    if (([uri]$Uri).Scheme -ne 'https') { throw 'Installer downloads must use HTTPS.' }
    Write-Host "Downloading $Name from the official source..." -ForegroundColor Cyan
    $response = $null
    $inputStream = $null
    $file = $null
    $complete = $false
    $createdFile = $false
    try {
        Write-Progress -Id 1 -Activity "Downloading $Name" -Status 'Connecting to the download server...' -PercentComplete -1
        $response = (New-DownloadRequest ([uri]$Uri)).GetResponse()
        Assert-HttpsResponse $response
        $total = $response.ContentLength
        if ($total -gt 0) { Write-Host "Download size: $(Format-DownloadSize $total). The installer opens when this finishes." }
        else { Write-Host 'The server did not report a total size. Showing the amount downloaded instead.' }
        $inputStream = $response.GetResponseStream()
        $file = [IO.File]::Create($Destination)
        $createdFile = $true
        $buffer = New-Object byte[] 65536
        $received = 0L
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $lastUpdate = -1.0
        while (($count = $inputStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
            $file.Write($buffer, 0, $count)
            $received += $count
            $seconds = $timer.Elapsed.TotalSeconds
            if ($seconds -gt 1800) { throw 'Download exceeded 30 minutes. Check your connection and rerun.' }
            if (($seconds - $lastUpdate) -ge 0.25) {
                $speed = Format-DownloadSize ([long]($received / [math]::Max($seconds, 0.001)))
                $status = "$(Format-DownloadSize $received) downloaded | $speed/s"
                $percent = -1
                if ($total -gt 0) {
                    $percent = [int][math]::Min(99, [math]::Floor(100.0 * $received / $total))
                    $status = "$percent% | $(Format-DownloadSize $received) of $(Format-DownloadSize $total) | $speed/s"
                }
                Write-Progress -Id 1 -Activity "Downloading $Name" -Status $status -PercentComplete $percent
                $lastUpdate = $seconds
            }
        }
        if ($received -eq 0 -or ($total -ge 0 -and $received -ne $total)) {
            throw 'The download ended before the complete installer arrived. Rerun to try again.'
        }
        $file.Flush()
        $complete = $true
    } finally {
        if ($null -ne $file) { $file.Dispose() }
        if ($null -ne $inputStream) { $inputStream.Dispose() }
        if ($null -ne $response) { $response.Dispose() }
        Write-Progress -Id 1 -Activity "Downloading $Name" -Completed
        if ($createdFile -and -not $complete) { Remove-Item -LiteralPath $Destination -Force }
    }
    Write-Host "Downloaded $Name ($(Format-DownloadSize $received))." -ForegroundColor Green
}
function Install-Applications {
    $script:Code = Get-Code
    if (-not $script:Code) {
        if (-not (Confirm-Step 'Download and run the official VS Code Windows x64 user installer (in your account)')) { throw 'VS Code is required.' }
        $installer = Join-Path $script:Work 'VSCodeUserSetup.exe'
        Save-Download 'https://update.code.visualstudio.com/latest/win32-x64-user/stable' $installer 'VS Code'
        Write-Host 'Checking the Microsoft installer signature...'
        Assert-SignedInstaller $installer 'Microsoft'
        Write-Host 'Opening the VS Code installer. Finish its setup window to continue.'
        $process = Start-Process -FilePath $installer -PassThru
        # Wait only for the installer, not editor/server processes it launches.
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) { throw "VS Code installer exited $($process.ExitCode)." }
        $script:Code = Get-Code
        if (-not $script:Code) { throw 'VS Code was not found after installation. Finish installing it and rerun.' }
    } else { Write-Host 'VS Code is already installed; no download needed.' -ForegroundColor Green }
    $versionLines = @(Invoke-NativeOutput $script:Code @('--version'))
    if ($versionLines.Count -eq 0) { throw 'Cannot read VS Code version.' }
    $version = [version]($versionLines[0].Trim())
    if ($version -lt [version]'1.127.0') { throw 'Update VS Code to 1.127+ for the official Ollama extension, then rerun.' }
    $script:Ollama = Get-Ollama
    if (-not $script:Ollama) {
        Write-Host "`nOllama is the engine that runs AI on your computer."
        Write-Host 'Its Windows installer includes the app and GPU-support libraries, so it is a large download.'
        Write-Host 'Allow at least 4 GB of disk space for Ollama itself. The AI model is a separate download later.'
        if (-not (Confirm-Step 'Download and run the official Ollama Windows installer (normally in your account)')) { throw 'Ollama is required.' }
        $installer = Join-Path $script:Work 'OllamaSetup.exe'
        Save-Download 'https://ollama.com/download/OllamaSetup.exe' $installer 'Ollama'
        Write-Host 'Checking the Ollama installer signature...'
        Assert-SignedInstaller $installer 'Ollama'
        Write-Host 'Opening the Ollama installer. Finish its setup window to continue.'
        $process = Start-Process -FilePath $installer -PassThru
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) { throw "Ollama installer exited $($process.ExitCode)." }
        $script:Ollama = Get-Ollama
        if (-not $script:Ollama) { throw 'Ollama was not found after installation. Finish installing it and rerun.' }
    } else { Write-Host 'Ollama is already installed; no installer download needed.' -ForegroundColor Green }
    Invoke-Native $script:Ollama @('--version')
}
function Assert-SignedInstaller([string]$Path, [string]$Publisher) {
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch [regex]::Escape($Publisher)) {
        throw "Installer signature is not valid for $Publisher. Refusing to run $Path."
    }
}
function Invoke-OllamaApi([string]$Endpoint, $Body = $null) {
    $request = [Net.WebRequest]::Create("http://127.0.0.1:11434/api/$Endpoint")
    $request.Proxy = $null
    $request.Timeout = 180000
    $request.ReadWriteTimeout = 180000
    if ($null -ne $Body) {
        $request.Method = 'POST'
        $request.ContentType = 'application/json'
        $bytes = [Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Depth 12 -Compress))
        $request.ContentLength = $bytes.Length
        $stream = $request.GetRequestStream()
        try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
    }
    $response = $request.GetResponse()
    try {
        $reader = [IO.StreamReader]::new($response.GetResponseStream())
        try { return $reader.ReadToEnd() | ConvertFrom-Json }
        finally { $reader.Dispose() }
    } finally { $response.Dispose() }
}
function Wait-Ollama {
    try { $response = Invoke-OllamaApi 'version' }
    catch {
        Write-Host 'Ollama is not responding at 127.0.0.1:11434. Starting it automatically...'
        try {
            [void](Start-Process -FilePath $script:Ollama -ArgumentList @('serve') -WindowStyle Hidden)
        } catch {
            throw "Ollama is installed but could not be started automatically: $($_.Exception.Message)"
        }
        $response = $null
        for ($attempt = 1; $attempt -le 30 -and $null -eq $response; $attempt++) {
            Start-Sleep -Seconds 1
            try { $response = Invoke-OllamaApi 'version' } catch {}
        }
        if ($null -eq $response) { throw 'Ollama did not respond after an automatic start attempt. Open Ollama and rerun setup.' }
    }
    if (-not $response.version) { throw 'Invalid Ollama server response.' }
}
function Get-ModelVolume {
    $path = $script:ModelStorage
    while (-not (Test-Path -LiteralPath $path -PathType Container)) {
        $parent = Split-Path -Parent $path
        if (-not $parent -or $parent -eq $path) { throw 'Cannot find the model storage volume.' }
        $path = $parent
    }
    $root = [IO.Path]::GetPathRoot((Get-Item -LiteralPath $path).FullName)
    $drive = [IO.DriveInfo]::new($root)
    return $drive
}
function Get-PromptPath {
    $documents = [Environment]::GetFolderPath([Environment+SpecialFolder]::MyDocuments)
    if (-not $documents) { $documents = Join-Path $HOME 'Documents' }
    [void][IO.Directory]::CreateDirectory($documents)
    return Join-Path $documents 'Local-AI-Gemini-Prompt.txt'
}
function Write-HardwarePrompt {
    $osInfo = Get-CimInstance Win32_OperatingSystem
    $processors = @(Get-CimInstance Win32_Processor | Select-Object -ExpandProperty Name)
    $visibleBytes = (Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory
    $modules = @(Get-CimInstance Win32_PhysicalMemory -Property Capacity)
    if ($modules.Count -eq 0) {
        throw 'Installed RAM capacity could not be read from Windows memory-device records. Check your hardware information.'
    }
    $ramBytes = ($modules | Measure-Object -Property Capacity -Sum).Sum
    if ($null -eq $ramBytes -or $ramBytes -le 0) {
        throw 'Installed RAM capacity could not be read from Windows memory-device records. Do not treat Windows-visible memory as installed capacity; check your hardware information.'
    }
    $ram = [math]::Round($ramBytes / 1GB, 1)
    $visibleRam = [math]::Round($visibleBytes / 1GB, 1)
    $sharedCeiling = [math]::Round($ramBytes / 2GB, 1)
    $gpuNames = @(Get-CimInstance Win32_VideoController | Select-Object -ExpandProperty Name)
    $gpu = $gpuNames -join '; '
    $vram = 'Unknown; Windows AdapterRAM is deliberately not used'
    $acceleration = 'Not established; verify CPU/GPU offload with ollama ps'
    $nvidia = Get-Command nvidia-smi.exe -ErrorAction SilentlyContinue
    if ($nvidia) {
        $report = @(& $nvidia.Source '--query-gpu=name,memory.total,driver_version' '--format=csv,noheader')
        if ($LASTEXITCODE -eq 0) {
            $gpu = $report -join '; '
            $vram = 'See NVIDIA report (dedicated MiB per GPU, followed by driver)'
            $acceleration = 'NVIDIA driver responds; actual offload still needs checking'
        } else { Write-Warning 'nvidia-smi failed. GPU memory/acceleration is unknown.' }
    }
    Write-Host "GPU information: $gpu"
    Write-Host "Installed RAM: $ram GiB. Windows-visible RAM: $visibleRam GiB (not installed capacity or currently free memory)."
    Write-Host "For iGPU planning, 50% of installed RAM is $sharedCeiling GiB; actual availability depends on Windows and the driver."
    $free = [math]::Round((Get-ModelVolume).AvailableFreeSpace / 1GB, 1)
    $candidates = @(Get-VerifiedCandidateCatalog)
    $candidateLines = @($candidates | ForEach-Object {
        $candidateSize = ($_.DownloadBytes / 1GB).ToString('0.0', [Globalization.CultureInfo]::InvariantCulture)
        "- $($_.Model) | DOWNLOAD_GIB $candidateSize | VERIFIED_CONTEXT_TOKENS $($_.ContextLength)"
    }) -join "`n"
    $promptText = @"
Recommend a practical LOCAL Ollama coding model for VS Code Agent mode.
OS: $($osInfo.Caption), version $($osInfo.Version); architecture: x64
CPU: $($processors -join '; ')
Installed physical RAM: $ram GiB (sum of memory-device capacities)
Windows-visible physical RAM: $visibleRam GiB (not installed capacity or currently free RAM)
GPU: $gpu
GPU memory: $vram
Windows iGPU planning ceiling: up to 50% of installed RAM ($sharedCeiling GiB), shared dynamically; actual availability unverified
Acceleration: $acceleration
Free model-volume space: $free GiB

WIZARD-VERIFIED EXACT-TAG CANDIDATES:
$candidateLines

These are the ONLY allowed Ollama Library recommendations for this run. The
wizard read each exact tag's registry layers and GGUF context metadata directly.
Choose MODEL exactly as written from this list, or use MODEL: NONE. Do not output
another tag, alias, quantization, family member or Hugging Face model in the
copy-back line. Candidate size and verified context are metadata for your
analysis; the wizard already has them. Tool use is still unverified until the
wizard's post-download local metadata and structured tool-call checks pass.

TOOL CALLING STATUS:
The shortlist is limited to models currently presented as tool-capable by
Ollama, but that label is not proof. Do not claim success; the wizard must see
an actual structured Ollama API tool_calls response from the downloaded exact tag.

MEMORY ACCOUNTING RULES:
If this machine uses an integrated GPU/APU, use installed RAM above as ONE physical
pool. Up to 50% of installed RAM could be made available for running Ollama models;
use that as a planning ceiling, NOT a fixed reservation, currently free memory,
extra dedicated VRAM, or a guaranteed driver/Ollama allocation.
Installed capacity and Windows-visible memory are DIFFERENT measurements.
Do not relabel Windows-visible memory as installed capacity. If they differ,
the cause is unknown here; do not infer that the difference is GPU-reserved or
currently available to the model. Do not assume all installed RAM is presently usable by Windows or Ollama.
Reconcile visible-memory and actual driver/runtime limits before claiming a model fits.
Do not infer a BIOS carve-out or fixed GPU allocation from the GPU model name.
Do not add shared GPU memory to RAM or subtract it as a permanently reserved pool.
Budget model weights, KV cache at 65536+ context, runtime overhead and OS/editor
headroom together. Do not claim exact remaining/free RAM or no swapping without
actual usage measurements. Do not promise full GPU offload or fast generation
without confirming the backend, driver and actual Ollama runtime allocation.
This 50% planning rule is for Windows iGPUs, not a dedicated-GPU VRAM estimate.
Keep separately detected dedicated GPU memory separate; do not combine pools.

REQUIRED RESPONSE FORMAT: Start your response with this copy-back line, before
any greeting, explanation, heading, or analysis:
MODEL: <exact local Ollama name:tag; no command, URL, backticks, or cloud model>
This is the only value the user must copy into the setup wizard. The wizard
already knows the exact registry size and fixes agent context at 65536 tokens.
Put ALL analysis, links, caveats and fallback choices AFTER this line.
Do not invent a runnable recommendation if none is suitable:
use MODEL: NONE and explain the blocker afterward.

Compare ONLY the verified candidates above and explain which best fits this
hardware. Recommend a primary and smaller fallback from that same list, with
official model-page links, license/commercial-use caveats, realistic speed
expectations, and memory headroom for OS, VS Code, model weights AND KV cache.
Account for shared versus dedicated memory; do not add GPUs' VRAM together.
Do not suggest cloud inference. Do not claim structured tool calling is verified:
the wizard will test the exact downloaded model locally.
MANDATORY FOR THIS SETUP: We need at least 65536 tokens of context for the
VS Code Ollama extension. Recommend only models whose supported context window
is at least 65536 AND whose weights plus KV cache at that context fit this
hardware with OS/VS Code memory headroom. A small download alone is not enough.
Do not recommend a 32768-context model and simply set num_ctx to 65536; setting
a larger number does not establish model support. Verify any extended-context
requirements against the exact model variant. Do not lower context to make it fit.
If no suitable model fits, use MODEL: NONE and explain the hardware/support blocker.
Prefer https://ollama.com/library models. If none fit, give an exact supported
GGUF file and source page on https://huggingface.co, including architecture and
chat-template/tool support caveats. Do not suggest executing repository code.
State uncertainties and ask about missing hardware details instead of guessing.
"@
    $path = Get-PromptPath
    [IO.File]::WriteAllText($path, $promptText)
    Write-Host "`nReview this hardware-only prompt before sharing it with Gemini:`n$promptText"
    Write-Host "`nSaved to $path"
    if (Get-Command Set-Clipboard -ErrorAction SilentlyContinue) {
        try {
            Set-Clipboard -Value $promptText
            Write-Host 'Copied the prompt to the clipboard.'
        } catch {
            Write-Warning 'Could not copy the prompt automatically; use the displayed or saved copy.'
        }
    } else {
        Write-Host 'Clipboard command unavailable; use the displayed or saved copy.'
    }
}
function Test-ModelName([string]$Name) {
    return $Name -cmatch '^[a-zA-Z0-9][a-zA-Z0-9._-]*(:[a-zA-Z0-9][a-zA-Z0-9._-]*)?$' -and $Name -notmatch 'cloud'
}
function Get-CanonicalModelName([string]$Name) {
    if ($Name.Contains(':')) { return $Name }
    return "${Name}:latest"
}
function Test-ModelInstalled([string]$Name) {
    $wanted = Get-CanonicalModelName $Name
    $rows = @(& $script:Ollama list)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot list installed Ollama models.' }
    foreach ($row in $rows) {
        $columns = @(([string]$row).Trim() -split '\s+')
        if ($columns.Count -gt 0 -and $columns[0] -ieq $wanted) { return $true }
    }
    return $false
}
function Get-HttpsBytes([string]$Uri, [string]$Accept = '', [int]$RangeEnd = -1) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $parsed = [uri]$Uri
    if ($parsed.Scheme -ne 'https' -or $parsed.Host -ne 'registry.ollama.ai') {
        throw 'Registry metadata must come from registry.ollama.ai over HTTPS.'
    }
    $request = New-DownloadRequest $parsed
    if ($Accept) { $request.Accept = $Accept }
    if ($RangeEnd -ge 0) { $request.AddRange(0, $RangeEnd) }
    $response = $request.GetResponse()
    try {
        Assert-HttpsResponse $response
        if ($RangeEnd -ge 0 -and
            ([int]$response.StatusCode -ne 206 -or $response.ContentLength -lt 0 -or $response.ContentLength -gt ($RangeEnd + 1))) {
            throw 'Registry did not honor the bounded GGUF metadata request.'
        }
        $stream = $response.GetResponseStream()
        try {
            $memory = [IO.MemoryStream]::new()
            try {
                $stream.CopyTo($memory)
                return ,$memory.ToArray()
            } finally { $memory.Dispose() }
        } finally { $stream.Dispose() }
    } finally { $response.Dispose() }
}
function Read-GgufUInt32([byte[]]$Bytes, [ref]$Offset) {
    if ($Offset.Value -lt 0 -or $Offset.Value + 4 -gt $Bytes.Length) { throw 'GGUF metadata is incomplete.' }
    $value = [BitConverter]::ToUInt32($Bytes, $Offset.Value)
    $Offset.Value += 4
    return [uint64]$value
}
function Read-GgufUInt64([byte[]]$Bytes, [ref]$Offset) {
    if ($Offset.Value -lt 0 -or $Offset.Value + 8 -gt $Bytes.Length) { throw 'GGUF metadata is incomplete.' }
    $value = [BitConverter]::ToUInt64($Bytes, $Offset.Value)
    $Offset.Value += 8
    return $value
}
function Read-GgufString([byte[]]$Bytes, [ref]$Offset) {
    $length = Read-GgufUInt64 $Bytes $Offset
    if ($length -gt [int]::MaxValue -or $Offset.Value + [int]$length -gt $Bytes.Length) { throw 'GGUF metadata is incomplete.' }
    $value = [Text.Encoding]::UTF8.GetString($Bytes, $Offset.Value, [int]$length)
    $Offset.Value += [int]$length
    return $value
}
function Skip-GgufValue([byte[]]$Bytes, [ref]$Offset, [uint32]$Type) {
    $sizes = @{ 0 = 1; 1 = 1; 2 = 2; 3 = 2; 4 = 4; 5 = 4; 6 = 4; 7 = 1; 10 = 8; 11 = 8; 12 = 8 }
    if ($sizes.ContainsKey([int]$Type)) {
        $Offset.Value += $sizes[[int]$Type]
        if ($Offset.Value -gt $Bytes.Length) { throw 'GGUF metadata is incomplete.' }
        return
    }
    if ($Type -eq 8) {
        [void](Read-GgufString $Bytes $Offset)
        return
    }
    if ($Type -eq 9) {
        $elementType = [uint32](Read-GgufUInt32 $Bytes $Offset)
        $count = Read-GgufUInt64 $Bytes $Offset
        if ($sizes.ContainsKey([int]$elementType)) {
            $skip = $count * $sizes[[int]$elementType]
            if ($skip -gt [int]::MaxValue -or $Offset.Value + [int]$skip -gt $Bytes.Length) { throw 'GGUF metadata is incomplete.' }
            $Offset.Value += [int]$skip
            return
        }
        if ($elementType -eq 8) {
            for ($i = 0; $i -lt $count; $i++) { [void](Read-GgufString $Bytes $Offset) }
            return
        }
    }
    throw "Unsupported GGUF metadata type $Type."
}
function Get-GgufContextLength([byte[]]$Bytes) {
    if ($Bytes.Length -lt 24 -or [Text.Encoding]::ASCII.GetString($Bytes, 0, 4) -cne 'GGUF') {
        throw 'The model data is not a GGUF.'
    }
    $offsetValue = 4
    $offset = [ref]$offsetValue
    $version = Read-GgufUInt32 $Bytes $offset
    if ($version -lt 2 -or $version -gt 3) { throw "Unsupported GGUF version $version." }
    [void](Read-GgufUInt64 $Bytes $offset)
    $metadataCount = Read-GgufUInt64 $Bytes $offset
    for ($i = 0; $i -lt $metadataCount; $i++) {
        $key = Read-GgufString $Bytes $offset
        $type = [uint32](Read-GgufUInt32 $Bytes $offset)
        if ($key -match '\.context_length$') {
            if ($type -eq 4) { return [long](Read-GgufUInt32 $Bytes $offset) }
            if ($type -eq 10) { return [long](Read-GgufUInt64 $Bytes $offset) }
            throw "GGUF context length has unsupported metadata type $type."
        }
        Skip-GgufValue $Bytes $offset $type
    }
    return $null
}
function Get-RemoteOllamaModelInfo([string]$Name) {
    $canonical = Get-CanonicalModelName $Name
    $parts = $canonical.Split(':', 2)
    $manifestUri = "https://registry.ollama.ai/v2/library/$($parts[0])/manifests/$($parts[1])"
    try {
        $manifestBytes = Get-HttpsBytes $manifestUri 'application/vnd.docker.distribution.manifest.v2+json'
        $manifest = [Text.Encoding]::UTF8.GetString($manifestBytes) | ConvertFrom-Json
        $layers = @($manifest.layers)
        $modelLayer = @($layers | Where-Object { $_.mediaType -eq 'application/vnd.ollama.image.model' })
        if ($modelLayer.Count -ne 1 -or $modelLayer[0].digest -notmatch '^sha256:[0-9a-f]{64}$') {
            throw 'Registry manifest did not contain one local model layer.'
        }
        $downloadBytes = ($layers | Measure-Object -Property size -Sum).Sum
        if ($null -eq $downloadBytes -or [long]$downloadBytes -le 0) { throw 'Registry manifest did not report a download size.' }
        $blobUri = "https://registry.ollama.ai/v2/library/$($parts[0])/blobs/$($modelLayer[0].digest)"
        $prefix = Get-HttpsBytes $blobUri 'application/octet-stream' 1048575
        $context = Get-GgufContextLength $prefix
        if ($null -eq $context -or $context -le 0) { throw 'Registry GGUF metadata did not report a context length.' }
        return [pscustomobject]@{
            Model = $canonical
            DownloadBytes = [long]$downloadBytes
            ContextLength = [long]$context
        }
    } catch {
        throw "Could not safely verify exact tag $canonical before download: $($_.Exception.Message) No model was downloaded."
    }
}
function Get-VerifiedCandidateCatalog {
    $shortlist = @(
        'llama3.1:8b',
        'mistral-nemo:12b',
        'gpt-oss:20b',
        'devstral:24b',
        'qwen3-coder:30b'
    )
    $script:VerifiedCandidates = @{}
    foreach ($candidate in $shortlist) {
        try {
            $info = Get-RemoteOllamaModelInfo $candidate
            if ($info.ContextLength -ge 65536) {
                $script:VerifiedCandidates[$info.Model] = $info
            }
        } catch {
            Write-Warning "Skipped candidate $candidate because exact-tag preflight failed: $($_.Exception.Message)"
        }
    }
    if ($script:VerifiedCandidates.Count -eq 0) {
        throw 'No exact-tag candidate could be verified from the Ollama registry. Check the connection and rerun; no model was downloaded.'
    }
    return @($script:VerifiedCandidates.Values | Sort-Object DownloadBytes)
}
function Get-VerifiedCandidateInfo([string]$Name) {
    $canonical = Get-CanonicalModelName $Name
    if ($null -eq $script:VerifiedCandidates -or -not $script:VerifiedCandidates.ContainsKey($canonical)) {
        throw "Model $canonical was not in the wizard-verified candidate list given to Gemini. Use an exact listed tag or rerun for a fresh catalog. No model was downloaded."
    }
    return $script:VerifiedCandidates[$canonical]
}
function Get-ModelContextLimit($Metadata) {
    $limits = @()
    if ($Metadata.PSObject.Properties['details'] -and $Metadata.details.PSObject.Properties['context_length']) {
        $limits += [long]$Metadata.details.context_length
    }
    if ($Metadata.PSObject.Properties['model_info']) {
        $architecture = $Metadata.model_info.PSObject.Properties['general.architecture']
        if ($architecture) {
            $contextLimit = $Metadata.model_info.PSObject.Properties["$($architecture.Value).context_length"]
            if ($contextLimit) { $limits += [long]$contextLimit.Value }
        }
    }
    return $limits | Where-Object { $_ -gt 0 } | Sort-Object | Select-Object -First 1
}
function Assert-LocalModelCompatible($Metadata, [int]$Context) {
    foreach ($field in @('remote_host', 'remote_model')) {
        if ($Metadata.PSObject.Properties[$field] -and $Metadata.$field) { throw 'This model routes to cloud inference. Choose local model weights.' }
    }
    $limit = Get-ModelContextLimit $Metadata
    if ($null -ne $limit -and $Context -gt $limit) {
        throw "This model advertises a $limit-token context window, below the requested $Context. Choose an exact tag supporting at least 65536."
    }
    if ($null -eq $limit) { Write-Warning 'Ollama did not report a supported context limit after acquisition.' }
    if (-not $Metadata.PSObject.Properties['capabilities'] -or @($Metadata.capabilities) -notcontains 'tools') {
        throw 'Model does not advertise tool calling. Choose a tool-capable model.'
    }
    return $limit
}
function Select-LocalModel {
    Write-Host "`nPaste Gemini's exact Ollama model tag or Hugging Face URL."
    Write-Host 'Gemini is advisory. Before downloading, this wizard checks the exact Ollama tag, registry size, and GGUF context metadata.'
    Write-Host 'If the model is already installed, it is checked and reused without pulling it again.'
    $selection = Read-Choice 'MODEL: paste the exact Ollama model name/tag, or a huggingface.co URL for a GGUF import'
    $source = '1'
    $gguf = $null
    $installed = $false
    $remoteInfo = $null
    $script:Context = 65536
    if ($selection -match '^https://huggingface\.co/') {
        $source = '2'
        Write-Host 'Detected a Hugging Face source. Download a compatible single-file GGUF from that page yourself.'
        Write-Host 'Check its license, architecture, quantization and tool/chat-template support; arbitrary models may not work.'
        $gguf = Read-Choice 'Full path to the downloaded single-file .gguf'
    } elseif ([IO.Path]::IsPathRooted($selection) -and [IO.Path]::GetExtension($selection) -ieq '.gguf') {
        $source = '2'
        $gguf = $selection
        Write-Host 'Detected a local GGUF file.'
    } elseif ($selection -match '^https?://') {
        throw 'Only huggingface.co URLs are accepted for GGUF imports; otherwise paste an exact Ollama model name and tag.'
    } else {
        $script:Model = $selection
        Write-Host 'Detected an Ollama Library model name.'
    }
    if ($source -eq '2') {
        if (-not [IO.Path]::IsPathRooted($gguf) -or -not (Test-Path -LiteralPath $gguf -PathType Leaf) -or
            [IO.Path]::GetExtension($gguf) -ine '.gguf' -or $gguf -match '[\r\n]') { throw 'Choose an existing absolute .gguf path.' }
        $stream = [IO.File]::OpenRead($gguf)
        try {
            $bytes = New-Object byte[] 4
            $count = $stream.Read($bytes, 0, 4)
            if ($count -ne 4 -or [Text.Encoding]::ASCII.GetString($bytes) -cne 'GGUF') { throw 'The file is not a GGUF.' }
        } finally { $stream.Dispose() }
        $baseName = [IO.Path]::GetFileNameWithoutExtension($gguf).ToLowerInvariant()
        $safeName = [regex]::Replace($baseName, '[^a-z0-9._-]+', '-').Trim('-')
        if (-not $safeName) { $safeName = 'model' }
        $script:Model = "imported-$safeName"
        Write-Host "Import name: $script:Model"
    }
    if ($script:Model -eq 'NONE') { throw 'Gemini found no suitable local model for this hardware at 65536+ context. Review its explanation; do not download an unsuitable model.' }
    if (-not (Test-ModelName $script:Model)) { throw 'Use a simple model[:tag], not a command, URL, namespace or cloud model.' }
    $script:LocalModel = 'local-ai-' + $script:Model.Replace(':', '-')
    $modelfile = Join-Path $script:Work 'Modelfile'
    if ($source -eq '1') {
        $remoteInfo = Get-VerifiedCandidateInfo $script:Model
        $installed = Test-ModelInstalled $script:Model
        if ($installed) {
            $metadata = Invoke-OllamaApi 'show' @{ model = $script:Model }
            $limit = Assert-LocalModelCompatible $metadata $script:Context
            Write-Host "`nVerified review:" -ForegroundColor Cyan
            Write-Host "  Exact model: $(Get-CanonicalModelName $script:Model)"
            Write-Host '  Download: already installed; no pull needed'
            Write-Host "  Reported context: $(if ($null -eq $limit) { 'unknown after local check' } else { "$limit tokens" })"
            Write-Host "  VS Code alias: $script:LocalModel (created/recreated after confirmation)"
            Write-Host '  Tools: advertised locally; structured tool use is tested next'
        } else {
            if ($remoteInfo.ContextLength -lt $script:Context) {
                throw "Exact tag $($remoteInfo.Model) advertises $($remoteInfo.ContextLength) context tokens, below the requested $script:Context. No model was downloaded."
            }
            if ((Get-ModelVolume).AvailableFreeSpace -lt ($remoteInfo.DownloadBytes + 2GB)) {
                throw "Not enough model-volume space for the verified $(Format-DownloadSize $remoteInfo.DownloadBytes) download plus 2 GiB spare. No model was downloaded."
            }
            Write-Host "`nVerified review:" -ForegroundColor Cyan
            Write-Host "  Exact model: $($remoteInfo.Model)"
            Write-Host "  Registry download: $(Format-DownloadSize $remoteInfo.DownloadBytes)"
            Write-Host "  GGUF context limit: $($remoteInfo.ContextLength) tokens"
            Write-Host "  Requested agent context: $script:Context tokens"
            Write-Host "  Free model-volume space: $(Format-DownloadSize (Get-ModelVolume).AvailableFreeSpace)"
            Write-Host "  VS Code alias: $script:LocalModel (created/recreated after confirmation)"
            Write-Host '  Tools: not trusted yet; checked from local metadata and a live tool call after download'
        }
    } else {
        $prefixLength = [int][math]::Min(1MB, (Get-Item -LiteralPath $gguf).Length)
        $stream = [IO.File]::OpenRead($gguf)
        try {
            $prefix = New-Object byte[] $prefixLength
            [void]$stream.Read($prefix, 0, $prefixLength)
        } finally { $stream.Dispose() }
        try { $ggufLimit = Get-GgufContextLength $prefix } catch { $ggufLimit = $null }
        if ($null -ne $ggufLimit -and $ggufLimit -lt $script:Context) {
            throw "This GGUF advertises $ggufLimit context tokens, below the requested $script:Context."
        }
        $ggufBytes = (Get-Item -LiteralPath $gguf).Length
        if ((Get-ModelVolume).AvailableFreeSpace -lt ($ggufBytes + 2GB)) {
            throw 'Not enough model-volume space for another GGUF-sized copy plus 2 GiB spare.'
        }
        Write-Host "`nReview:" -ForegroundColor Cyan
        Write-Host "  Import name: $(Get-CanonicalModelName $script:Model)"
        Write-Host "  GGUF file: $([math]::Round($ggufBytes / 1GB, 1)) GiB"
        Write-Host "  GGUF context: $(if ($null -eq $ggufLimit) { 'not found; Ollama will recheck after import' } else { "$ggufLimit tokens" })"
        Write-Host "  Requested agent context: $script:Context tokens"
        Write-Host "  VS Code alias: $script:LocalModel (created/recreated after confirmation)"
    }
    if (-not (Confirm-Step "Proceed with $script:Model at $script:Context context")) { throw 'Model acquisition cancelled.' }
    if ($source -eq '1') {
        if ($installed) { Write-Host 'Using the already-installed model; no download or update was performed.' -ForegroundColor Green }
        else { Invoke-Native $script:Ollama @('pull', $script:Model) }
    } else {
        $escaped = $gguf.Replace('\', '\\').Replace('"', '\"')
        [IO.File]::WriteAllText($modelfile, "FROM `"$escaped`"`n")
        Invoke-Native $script:Ollama @('create', $script:Model, '-f', $modelfile)
    }
    $metadata = Invoke-OllamaApi 'show' @{ model = $script:Model }
    [void](Assert-LocalModelCompatible $metadata $script:Context)
    [IO.File]::WriteAllText($modelfile, "FROM $script:Model`nPARAMETER num_ctx $script:Context`n")
    Invoke-Native $script:Ollama @('create', $script:LocalModel, '-f', $modelfile)
}
function Test-LocalModel([int]$OutputTokens = 1024) {
    $script:ToolCheckPassed = $false
    $body = @{
        model = $script:LocalModel; stream = $false
        options = @{ num_ctx = $script:Context; num_predict = $OutputTokens; temperature = 0 }
        messages = @(@{ role = 'user'; content = 'What is the temperature in New York?' })
        tools = @(@{
            type = 'function'
            function = @{
                name = 'get_temperature'; description = 'Get the current temperature for a city'
                parameters = @{
                    type = 'object'; properties = @{ city = @{ type = 'string'; description = 'The name of the city' } }
                    required = @('city')
                }
            }
        })
    }
    Write-Host 'Checking local inference and a harmless tool request (180-second limit; no tool will execute)...'
    $response = Invoke-OllamaApi 'chat' $body
    $valid = $false
    if ($response.PSObject.Properties['done'] -and $response.done -eq $true -and
        $response.PSObject.Properties['message'] -and $response.message.PSObject.Properties['tool_calls']) {
        foreach ($call in $response.message.tool_calls) {
            if ($call.PSObject.Properties['function'] -and $call.function.PSObject.Properties['name'] -and
                $call.function.name -ceq 'get_temperature' -and $call.function.PSObject.Properties['arguments'] -and
                $call.function.arguments.PSObject.Properties['city'] -and $call.function.arguments.city -ieq 'New York') { $valid = $true }
        }
    }
    if (-not $valid) {
        $reason = 'not reported'
        if ($response.PSObject.Properties['done_reason']) { $reason = [string]$response.done_reason }
        $detail = 'No matching tool call was returned.'
        if ($reason -eq 'length') {
            $detail = "The model reached the $OutputTokens-token output limit before completing the check. Reasoning may have used that budget."
        }
        throw [IO.InvalidDataException]::new(
            "Model $script:LocalModel downloaded successfully, but tool use is NOT verified. $detail Stop reason: $reason. A reply in Ollama chat alone does not prove Agent tool support.")
    }
    $script:ToolCheckPassed = $true
    Write-Host 'Local tool-call check passed. Actual runtime/offload:'
    Invoke-Native $script:Ollama @('ps')
    Write-Host 'CPU-only offload can be slow even with a GPU installed. No GPU drivers were changed.'
}
function Confirm-LocalModelTools {
    $budget = 1024
    while ($true) {
        try { Test-LocalModel -OutputTokens $budget; return }
        catch [IO.InvalidDataException] {
            Write-Warning $_.Exception.Message
            Write-Host 'Your downloaded model is still available; you do not need to download it again.'
            Write-Host '1 = retry with a larger output budget (2048 tokens; may take longer)'
            Write-Host '2 = continue to VS Code with tool use UNVERIFIED; test an approved edit there'
            Write-Host 'q = stop and rerun with a different tool-capable model'
            switch (Read-Choice 'Choose 1 or 2') {
                '1' { $budget = 2048 }
                '2' {
                    Write-Warning 'Continuing with tool use UNVERIFIED. The model may not work in Agent mode; no tool check has passed.'
                    return
                }
                default { throw 'Choose 1 or 2, or q to stop.' }
            }
        }
    }
}
function Wait-CodeProfile([int]$Attempts = 15) {
    $lastError = $null
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            return @(Invoke-NativeOutput $script:Code @('--profile', 'Local AI', '--list-extensions'))
        } catch {
            $lastError = $_.Exception.Message
            if ($attempt -lt $Attempts) {
                if ($attempt -eq 1) { Write-Host 'Waiting for VS Code to finish creating the Local AI profile...' }
                Start-Sleep -Seconds 1
            }
        }
    }
    throw "VS Code did not make the Local AI profile available after opening it. Last error:`n$lastError"
}
function Connect-Code {
    if ($script:ToolCheckPassed) {
        Write-Host 'The local tool-call check passed. VS Code Agent behavior still needs your confirmation.'
    } else {
        Write-Warning 'Local tool use is UNVERIFIED. Test an approved edit in VS Code; choose another model if Agent tools fail.'
    }
    Write-Host 'GitHub Copilot Chat is bundled with this supported VS Code version; no marketplace install is needed.'
    Write-Host 'Creating/reusing Local AI and installing Ollama.ollama for that profile.'
    Invoke-Native $script:Code @('--new-window', '--profile', 'Local AI')
    $extensions = @(Wait-CodeProfile)
    if ($extensions -contains 'Ollama.ollama') {
        Write-Host 'The Ollama extension is already installed in Local AI; no reinstall needed.' -ForegroundColor Green
    } else {
        Invoke-Native $script:Code @('--profile', 'Local AI', '--install-extension', 'Ollama.ollama')
        $extensions = @(Invoke-NativeOutput $script:Code @('--profile', 'Local AI', '--list-extensions'))
    }
    if ($extensions -notcontains 'Ollama.ollama') {
        throw 'The Ollama extension was not installed in the Local AI profile.'
    }
    Write-Host @"

Finish in the Local AI window (these UI steps are not automated):
1. Open or clone a repository: use File > Open Folder, File > Open Recent, or
   Source Control > Clone Repository. Trust only repositories you trust.
2. Open the built-in Copilot Chat, select Agent mode, then $script:LocalModel under Ollama.
   Do NOT select Auto or a cloud model. If missing, use the Command Palette:
   Ollama: Refresh Models, or Ollama: Diagnose Models.
   If those commands do not appear yet, run Developer: Reload Window once.
3. In this profile's Settings, enable chat.agentHost.byokModels.enabled if
   required for the Copilot harness. Set chat.utilityModel and
   chat.utilitySmallModel to your local model using their model pickers.
   Verify Ollama's endpoint is http://127.0.0.1:11434. Remote provider
   configurations take precedence; remove obsolete built-in Ollama configs.
4. Ask: "Suggest one small documentation improvement, apply it after approval,
   then run an existing relevant test if available." Review tool approvals and
   the resulting diff; keep changes only if you want them.
5. For metered cloud Copilot: Manage (gear) > Profiles > your previous profile
   (usually Default), then select a cloud model in Chat. Your normal account,
   plan and organization policy apply. Return via Profiles > Local AI and
   explicitly select $script:LocalModel again. Profiles are not billing boundaries.

VS Code Agent behavior still needs your confirmation.
Local inference has no per-request cloud-model fee but uses your hardware.
Other Copilot features, including gray-text completions, are separate.
No offline, telemetry, firewall, auto-approval or update settings were changed.
"@
}
function Start-LocalAISetup {
    if ($env:OS -ne 'Windows_NT' -or [Console]::IsInputRedirected -or [Console]::IsOutputRedirected) {
        throw 'Run this downloaded script in an interactive Windows PowerShell terminal.'
    }
    $principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run in your normal, non-administrator desktop terminal.'
    }
    $osInfo = Get-CimInstance Win32_OperatingSystem
    if ([version]$osInfo.Version -lt [version]'10.0.19045') { throw 'Windows 10 22H2 or newer is required.' }
    $architecture = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    if ($architecture -ne 'AMD64') { throw 'This wizard currently automates Windows x64 only; ARM64 requires a separately validated install path.' }
    Write-Host "`nLOCAL AI SETUP" -ForegroundColor Cyan
    Write-Host 'Four steps: install apps, choose a model, check it, connect VS Code.'
    Write-Host 'We ask before installing or downloading models. Enter q at a prompt to stop.'
    $script:Work = Join-Path ([IO.Path]::GetTempPath()) ('local-ai-setup-' + [guid]::NewGuid().ToString('N'))
    [void][IO.Directory]::CreateDirectory($script:Work)
    $previousHost = $env:OLLAMA_HOST
    $env:OLLAMA_HOST = '127.0.0.1:11434'
    try {
        Write-SetupStep 1 'Install or reuse VS Code and Ollama'
        Install-Applications
        Wait-Ollama
        Write-SetupStep 2 'Choose and download a local Ollama model'
        Write-Host 'First, review your hardware and ask Gemini which small, tool-capable model fits.'
        Write-Host 'Then choose it from the Ollama catalog. This wizard will download it for you.'
        $script:ModelStorage = if ($env:OLLAMA_MODELS) { $env:OLLAMA_MODELS } else { Join-Path $HOME '.ollama\models' }
        Write-Host "Detected model storage: $script:ModelStorage"
        Write-HardwarePrompt
        Select-LocalModel
        Write-SetupStep 3 'Check that the local model can use tools'
        Confirm-LocalModelTools
        Write-SetupStep 4 'Connect VS Code and open your repository'
        Connect-Code
    } finally {
        $env:OLLAMA_HOST = $previousHost
        Remove-Item -LiteralPath $script:Work -Recurse -Force
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    try { Start-LocalAISetup }
    catch [OperationCanceledException] { Write-Host 'Setup cancelled; rerun whenever ready.'; exit 0 }
    catch { Write-Error "Setup stopped: $($_.Exception.Message)" -ErrorAction Continue; exit 1 }
}
