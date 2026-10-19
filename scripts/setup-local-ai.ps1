# Native Windows PowerShell 5.1+; no Python or package-manager prerequisite.
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Read-Choice([string]$Message) {
    $answer = Read-Host "$Message (q to quit)"
    if ($answer -ceq 'q') { throw [OperationCanceledException]::new('Setup cancelled.') }
    return $answer
}
function Confirm-Step([string]$Message) {
    return (Read-Choice "$Message [y/N]") -match '^(y|yes)$'
}
function Invoke-Native([string]$File, [string[]]$Arguments) {
    & $File @Arguments | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "$File failed (exit $LASTEXITCODE). Resolve the error and rerun." }
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
    $versionLines = @(& $script:Code --version)
    if ($LASTEXITCODE -ne 0 -or $versionLines.Count -eq 0) { throw 'Cannot read VS Code version.' }
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
        Write-Host 'Ollama is not responding at 127.0.0.1:11434. Open Ollama from the Start menu.'
        [void](Read-Choice 'When it is running, press Enter to retry')
        $response = Invoke-OllamaApi 'version'
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
function Write-HardwarePrompt {
    $osInfo = Get-CimInstance Win32_OperatingSystem
    $processors = @(Get-CimInstance Win32_Processor | Select-Object -ExpandProperty Name)
    $ram = [math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB, 1)
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
    $manual = Read-Choice 'Optional GPU memory in GiB from the hardware specification (Enter if unknown)'
    if ($manual) {
        if ($manual -notmatch '^[0-9]+(\.[0-9]+)?$') { throw 'GPU memory must be numeric GiB.' }
        $vram = "$manual GiB (user supplied; not automatically verified)"
    }
    $free = [math]::Round((Get-ModelVolume).AvailableFreeSpace / 1GB, 1)
    $promptText = @"
Recommend a practical LOCAL Ollama coding model for VS Code Agent mode.
OS: $($osInfo.Caption), version $($osInfo.Version); architecture: x64
CPU: $($processors -join '; ')
RAM: $ram GiB
GPU: $gpu
GPU memory: $vram
Acceleration: $acceleration
Free model-volume space: $free GiB

Use current official sources and provide links, not an assumed universal optimum.
Recommend a primary and smaller fallback with reliable TOOL CALLING, exact LOCAL
Ollama Library tag, model-page/download link, quantization, download size in GiB,
license/commercial-use caveats, realistic speed expectations, and memory headroom
for OS, VS Code, model weights AND KV cache. Account for shared versus dedicated
memory; do not add GPUs' VRAM together. Recommend a practical context size for
agents and explain the quality/speed tradeoff. Do not suggest cloud inference.
The official VS Code Ollama extension recommends at least 65536 context tokens.
If that will not fit, say explicitly that smaller-context agents are constrained;
do not sacrifice OS/memory headroom or claim the machine meets that recommendation.
Prefer https://ollama.com/library models. If none fit, give an exact supported
GGUF file and source page on https://huggingface.co, including architecture and
chat-template/tool support caveats. Do not suggest executing repository code.
State uncertainties and ask about missing hardware details instead of guessing.
"@
    $path = Join-Path $script:State 'gemini-prompt.txt'
    [IO.File]::WriteAllText($path, $promptText)
    Write-Host "`nReview this hardware-only prompt before sharing it with Gemini:`n$promptText"
    Write-Host "`nSaved to $path"
    if (Confirm-Step 'Copy the reviewed prompt to the clipboard') { Set-Clipboard -Value $promptText }
    [void](Read-Choice 'Paste it into Gemini yourself; return with a model tag, size and context recommendation, then press Enter')
}
function Test-ModelName([string]$Name) {
    return $Name -cmatch '^[a-zA-Z0-9][a-zA-Z0-9._-]*(:[a-zA-Z0-9][a-zA-Z0-9._-]*)?$' -and $Name -notmatch 'cloud'
}
function Select-LocalModel {
    $source = Read-Choice 'Model source: 1 = Ollama Library (recommended), 2 = import a downloaded Hugging Face GGUF'
    $gguf = $null
    switch ($source) {
        '1' { $script:Model = Read-Choice 'Exact local Ollama Library tag from Gemini (not a URL or command)' }
        '2' {
            Write-Host 'Download a compatible single-file GGUF yourself. Check license, architecture, quantization and tool/chat-template support.'
            $gguf = Read-Choice 'Full path to the downloaded single-file .gguf'
            if (-not [IO.Path]::IsPathRooted($gguf) -or -not (Test-Path -LiteralPath $gguf -PathType Leaf) -or
                [IO.Path]::GetExtension($gguf) -ine '.gguf' -or $gguf -match '[\r\n]') { throw 'Choose an existing absolute .gguf path.' }
            $stream = [IO.File]::OpenRead($gguf)
            try {
                $bytes = New-Object byte[] 4
                $count = $stream.Read($bytes, 0, 4)
                if ($count -ne 4 -or [Text.Encoding]::ASCII.GetString($bytes) -cne 'GGUF') { throw 'The file is not a GGUF.' }
            } finally { $stream.Dispose() }
            $script:Model = Read-Choice 'Local name for this import (use a new name to preserve previous models)'
        }
        default { throw 'Choose 1 or 2.' }
    }
    if (-not (Test-ModelName $script:Model)) { throw 'Use a simple model[:tag], not a command, URL, namespace or cloud model.' }
    $sizeText = Read-Choice 'Expected additional model storage in GiB (include an extra copy for GGUF import; Enter if unknown)'
    $size = 0.0
    if ($sizeText) {
        if ($sizeText -notmatch '^[0-9]+(\.[0-9]+)?$') { throw 'Size must be numeric GiB.' }
        $size = [double]::Parse($sizeText, [Globalization.CultureInfo]::InvariantCulture)
    } else { Write-Warning 'Download size unknown. Check the model page against available disk space.' }
    if ((Get-ModelVolume).AvailableFreeSpace -lt (($size + 2) * 1GB)) { throw 'Not enough model-volume space (allowing 2 GiB spare). Free space or choose a smaller model.' }
    $contextText = Read-Choice 'Agent context tokens recommended by Gemini (Enter for conservative 8192)'
    if (-not $contextText) { $contextText = '8192' }
    if ($contextText -notmatch '^[0-9]{1,7}$' -or [int]$contextText -lt 2048 -or [int]$contextText -gt 1048576) {
        throw 'Choose an integer context size from 2048 to 1048576.'
    }
    $script:Context = [int]$contextText
    if ($script:Context -lt 65536) {
        Write-Warning 'The Ollama VS Code extension recommends 65536+ context tokens. A smaller budget saves memory but limits agents and may trigger a VS Code warning.'
    }
    if (-not (Confirm-Step "Acquire $script:Model with context ${script:Context}? Large downloads; existing models with this name may be updated/replaced")) { throw 'Model acquisition cancelled.' }
    $modelfile = Join-Path $script:Work 'Modelfile'
    if ($source -eq '1') {
        Invoke-Native $script:Ollama @('pull', $script:Model)
    } else {
        $escaped = $gguf.Replace('\', '\\').Replace('"', '\"')
        [IO.File]::WriteAllText($modelfile, "FROM `"$escaped`"`n")
        Invoke-Native $script:Ollama @('create', $script:Model, '-f', $modelfile)
    }
    $metadata = Invoke-OllamaApi 'show' @{ model = $script:Model }
    foreach ($field in @('remote_host', 'remote_model')) {
        if ($metadata.PSObject.Properties[$field] -and $metadata.$field) { throw 'This model routes to cloud inference. Choose local model weights.' }
    }
    if (-not $metadata.PSObject.Properties['capabilities'] -or @($metadata.capabilities) -notcontains 'tools') {
        throw 'Model does not advertise tool calling. Choose a tool-capable model.'
    }
    $script:LocalModel = 'local-ai-' + $script:Model.Replace(':', '-')
    [IO.File]::WriteAllText($modelfile, "FROM $script:Model`nPARAMETER num_ctx $script:Context`n")
    Invoke-Native $script:Ollama @('create', $script:LocalModel, '-f', $modelfile)
}
function Test-LocalModel {
    $body = @{
        model = $script:LocalModel; stream = $false
        options = @{ num_ctx = $script:Context; num_predict = 256; temperature = 0 }
        messages = @(@{ role = 'user'; content = 'Call local_ai_check with value ready. Do not answer in prose.' })
        tools = @(@{
            type = 'function'
            function = @{
                name = 'local_ai_check'; description = 'Harmless setup check. Call with value ready.'
                parameters = @{
                    type = 'object'; properties = @{ value = @{ type = 'string'; enum = @('ready') } }
                    required = @('value')
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
                $call.function.name -ceq 'local_ai_check' -and $call.function.PSObject.Properties['arguments'] -and
                $call.function.arguments.PSObject.Properties['value'] -and $call.function.arguments.value -ceq 'ready') { $valid = $true }
        }
    }
    if (-not $valid) { throw 'Model did not produce the expected tool call. Try a reliable tool-capable model or a compatible GGUF template.' }
    Write-Host 'Local tool-call check passed. Actual runtime/offload:'
    Invoke-Native $script:Ollama @('ps')
    Write-Host 'CPU-only offload can be slow even with a GPU installed. No GPU drivers were changed.'
}
function Connect-Code {
    if (-not (Confirm-Step 'Create/reuse Local AI and install official GitHub.copilot-chat and Ollama.ollama extensions only for that profile')) { throw 'Profile setup cancelled.' }
    Invoke-Native $script:Code @('--profile', 'Local AI', '--install-extension', 'GitHub.copilot-chat')
    Invoke-Native $script:Code @('--profile', 'Local AI', '--install-extension', 'Ollama.ollama')
    $extensions = @(& $script:Code --profile 'Local AI' --list-extensions)
    if ($LASTEXITCODE -ne 0 -or $extensions -notcontains 'GitHub.copilot-chat' -or $extensions -notcontains 'Ollama.ollama') {
        throw 'Required profile extensions were not installed.'
    }
    $repo = Read-Choice 'Full path to an existing locally checked-out repository'
    if (-not [IO.Path]::IsPathRooted($repo) -or -not (Test-Path -LiteralPath $repo -PathType Container)) { throw 'Choose an existing absolute directory.' }
    $git = Get-Command git.exe -CommandType Application -ErrorAction SilentlyContinue
    if (-not $git) { throw 'Git is required to verify the checkout. Install Git and rerun.' }
    Invoke-Native $git.Source @('-C', $repo, 'rev-parse', '--show-toplevel')
    Invoke-Native $script:Code @('--new-window', '--profile', 'Local AI', $repo)
    Write-Host @"

Finish in the Local AI window (these UI steps are not automated):
1. Trust the workspace only if you trust this repository.
2. Open Chat, select Agent mode, then $script:LocalModel under Ollama.
   Do NOT select Auto or a cloud model. If missing, use the Command Palette:
   Ollama: Refresh Models, or Ollama: Diagnose Models.
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

The model check passed; VS Code Agent behavior still needs your confirmation.
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
    $script:State = Join-Path $env:LOCALAPPDATA 'LocalAISetup'
    [void][IO.Directory]::CreateDirectory($script:State)
    $script:Work = Join-Path ([IO.Path]::GetTempPath()) ('local-ai-setup-' + [guid]::NewGuid().ToString('N'))
    [void][IO.Directory]::CreateDirectory($script:Work)
    $previousHost = $env:OLLAMA_HOST
    $env:OLLAMA_HOST = '127.0.0.1:11434'
    try {
        Write-SetupStep 1 'Install or reuse VS Code and Ollama'
        Install-Applications
        Wait-Ollama
        Write-SetupStep 2 'Find a model that fits your computer'
        $script:ModelStorage = if ($env:OLLAMA_MODELS) { $env:OLLAMA_MODELS } else { Join-Path $HOME '.ollama\models' }
        Write-Host "Model storage default: $script:ModelStorage"
        $storage = Read-Choice 'If your running Ollama server uses different model storage, enter its absolute directory (Enter to keep default)'
        if ($storage) {
            if (-not [IO.Path]::IsPathRooted($storage) -or -not (Test-Path -LiteralPath $storage -PathType Container)) {
                throw 'Choose an existing absolute model-storage directory.'
            }
            $script:ModelStorage = $storage
        }
        Write-HardwarePrompt
        Select-LocalModel
        Write-SetupStep 3 'Check that the local model can use tools'
        Test-LocalModel
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
    catch { Write-Error "Setup stopped: $($_.Exception.Message). Check Ollama logs for inference errors; try a smaller model/context if memory or timeout is the issue." -ErrorAction Continue; exit 1 }
}
