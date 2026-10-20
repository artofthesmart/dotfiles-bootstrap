# Standalone mocked tests; no Pester dependency, installations or model downloads.
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '../scripts/setup-local-ai.ps1'
$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($scriptPath, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
. $scriptPath

$script:Passed = 0
function Assert-True($Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function Assert-Throws([scriptblock]$Action, [string]$Pattern) {
    try { & $Action } catch {
        if ($_.Exception.Message -match $Pattern) { return }
        throw "Unexpected error: $($_.Exception.Message); expected $Pattern"
    }
    throw "Expected failure matching $Pattern"
}
function Test-Case([string]$Name, [scriptblock]$Action) {
    # Keep routine wizard transcripts out of the test summary.
    & $Action 6>$null
    $script:Passed++
    Write-Host "PASS: $Name"
}

$script:Fixture = Join-Path ([IO.Path]::GetTempPath()) ('local-ai-ps-tests-' + [guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($script:Fixture)
$script:Work = $script:Fixture
$script:State = $script:Fixture
$script:Code = 'fixture-code'
$script:Ollama = 'fixture-ollama'
$script:Context = 65536
$script:LocalModel = 'fixture-model'
$script:Answers = [Collections.Generic.Queue[string]]::new()
$script:Questions = [Collections.Generic.List[string]]::new()
$script:Calls = [Collections.Generic.List[object]]::new()
$script:Mode = 'ok'
$script:InstalledModel = $null
$script:ToolCheckPassed = $false
$script:PromptFolder = Join-Path $script:Fixture 'Documents'
function Get-PromptPath {
    [void][IO.Directory]::CreateDirectory($script:PromptFolder)
    return Join-Path $script:PromptFolder 'Local-AI-Gemini-Prompt.txt'
}

function Read-Choice([string]$Message) {
    $script:Questions.Add($Message)
    if ($script:Answers.Count -eq 0) { throw "Fixture ran out of answers: $Message" }
    $answer = $script:Answers.Dequeue()
    if ($answer -ceq 'q') { throw [OperationCanceledException]::new('Setup cancelled.') }
    return $answer
}
function Set-Answers([string[]]$Values) {
    $script:Answers.Clear()
    foreach ($value in $Values) { $script:Answers.Enqueue($value) }
}
function Invoke-Native([string]$File, [string[]]$Arguments) {
    $script:Calls.Add(@{ File = $File; Arguments = $Arguments })
    if ($Arguments -contains 'GitHub.copilot-chat') { throw 'Bundled Copilot Chat must not be installed.' }
    if ($script:Mode -eq 'pull-fails' -and $Arguments[0] -eq 'pull') { throw 'Download failed (fixture).' }
}
function Invoke-OllamaApi([string]$Endpoint, $Body = $null) {
    if ($script:Mode -eq 'http-error') { throw 'HTTP 500 (fixture)' }
    if ($Endpoint -eq 'show') {
        if ($script:Mode -eq 'remote') { return [pscustomobject]@{ remote_host = 'https://example.invalid'; capabilities = @('tools') } }
        if ($script:Mode -eq 'no-tools') { return [pscustomobject]@{ capabilities = @('completion') } }
        if ($script:Mode -eq 'info-context') {
            return [pscustomobject]@{ capabilities = @('tools'); model_info = [pscustomobject]@{ 'general.architecture' = 'qwen2'; 'qwen2.context_length' = 32768 } }
        }
        if ($script:Mode -eq 'unknown-context') { return [pscustomobject]@{ capabilities = @('tools') } }
        $limit = if ($script:Mode -eq 'short-context') { 32768 } else { 131072 }
        return [pscustomobject]@{ capabilities = @('completion', 'tools'); details = [pscustomobject]@{ context_length = $limit } }
    }
    if ($Endpoint -eq 'chat') {
        $script:LastBody = $Body
        if ($script:Mode -eq 'length' -and $Body.options.num_predict -lt 2048) {
            return [pscustomobject]@{ done = $true; done_reason = 'length'; message = [pscustomobject]@{ thinking = 'fixture reasoning' } }
        }
        if ($script:Mode -eq 'no-call') { return [pscustomobject]@{ done = $true; message = [pscustomobject]@{ content = 'hello' } } }
        if ($script:Mode -eq 'json-prose') {
            return [pscustomobject]@{ done = $true; message = [pscustomobject]@{ content = '{"name":"get_temperature","arguments":{"city":"New York"}}' } }
        }
        if ($script:Mode -eq 'malformed') { return [pscustomobject]@{ unexpected = 1 } }
        $value = if ($script:Mode -eq 'wrong-call') { 'wrong' } else { 'New York' }
        return [pscustomobject]@{ done = $true; message = [pscustomobject]@{ tool_calls = @(
            [pscustomobject]@{ function = [pscustomobject]@{ name = 'get_temperature'; arguments = [pscustomobject]@{ city = $value } } }
        ) } }
    }
    return [pscustomobject]@{ version = 'fixture' }
}
function Get-ModelVolume {
    $space = if ($script:Mode -eq 'low-disk') { 1GB } else { 100GB }
    return [pscustomobject]@{ AvailableFreeSpace = $space }
}
function Test-ModelInstalled([string]$Name) {
    return $null -ne $script:InstalledModel -and (Get-CanonicalModelName $Name) -ieq (Get-CanonicalModelName $script:InstalledModel)
}
function Get-RemoteOllamaModelInfo([string]$Name) {
    $context = if ($script:Mode -in @('short-context', 'info-context')) { 32768 } else { 131072 }
    return [pscustomobject]@{
        Model = Get-CanonicalModelName $Name
        DownloadBytes = 4GB
        ContextLength = $context
    }
}
function Get-VerifiedCandidateCatalog {
    $items = @(
        [pscustomobject]@{ Model = 'llama3.1:8b'; DownloadBytes = 5GB; ContextLength = 131072 },
        [pscustomobject]@{ Model = 'mistral-nemo:12b'; DownloadBytes = 7GB; ContextLength = 1024000 }
    )
    $script:VerifiedCandidates = @{}
    foreach ($item in $items) { $script:VerifiedCandidates[$item.Model] = $item }
    return $items
}
function Get-VerifiedCandidateInfo([string]$Name) {
    $canonical = Get-CanonicalModelName $Name
    if ($canonical -notin @('model:latest', 'model:q4')) {
        throw "Model $canonical was not in the wizard-verified candidate list given to Gemini. No model was downloaded."
    }
    return Get-RemoteOllamaModelInfo $Name
}
function Get-CimInstance([string]$ClassName, [string[]]$Property) {
    switch ($ClassName) {
        'Win32_OperatingSystem' { return [pscustomobject]@{ Caption = 'Fixture Windows'; Version = '10.0.22631'; CSName = 'DO-NOT-SHARE-HOST' } }
        'Win32_Processor' { return [pscustomobject]@{ Name = 'Fixture CPU'; ProcessorId = 'DO-NOT-SHARE-SERIAL' } }
        'Win32_ComputerSystem' { return [pscustomobject]@{ TotalPhysicalMemory = 32GB; UserName = 'DO-NOT-SHARE-USER' } }
        'Win32_PhysicalMemory' {
            if ($script:Mode -eq 'missing-installed-ram') { return @() }
            if ($script:Mode -eq '96gb-installed') {
                return @([pscustomobject]@{ Capacity = 48GB }, [pscustomobject]@{ Capacity = 48GB })
            }
            return [pscustomobject]@{ Capacity = 32GB }
        }
        'Win32_VideoController' { return [pscustomobject]@{ Name = 'Fixture GPU'; AdapterRAM = 1 } }
    }
}

try {
    Test-Case 'PowerShell source parses' { Assert-True ($null -ne $ast) 'Missing AST' }
    Test-Case 'Valid and invalid model identifiers' {
        Assert-True (Test-ModelName 'model:Q4_K_M') 'Valid tag rejected'
        foreach ($bad in @('x:cloud', 'CLOUD-model', '--help', 'a/b', 'https://a', 'a;whoami', '$(whoami)', '')) {
            Assert-True (-not (Test-ModelName $bad)) "Accepted unsafe model $bad"
        }
    }
    Test-Case 'GGUF context metadata parser reads exact limit' {
        $memory = [IO.MemoryStream]::new()
        $writer = [IO.BinaryWriter]::new($memory, [Text.Encoding]::UTF8, $true)
        try {
            $writer.Write([Text.Encoding]::ASCII.GetBytes('GGUF'))
            $writer.Write([uint32]3)
            $writer.Write([uint64]0)
            $writer.Write([uint64]1)
            $key = [Text.Encoding]::UTF8.GetBytes('qwen2.context_length')
            $writer.Write([uint64]$key.Length)
            $writer.Write($key)
            $writer.Write([uint32]4)
            $writer.Write([uint32]32768)
            $writer.Flush()
            Assert-True ((Get-GgufContextLength $memory.ToArray()) -eq 32768) 'Wrong GGUF context value'
        } finally {
            $writer.Dispose()
            $memory.Dispose()
        }
    }
    Test-Case 'Cancelled prompt is explicit' {
        Set-Answers @('q')
        Assert-Throws { Read-Choice 'fixture' } 'cancelled'
    }
    Test-Case 'HTTPS redirect and native failure details are enforced' {
        Assert-Throws {
            Assert-HttpsResponse ([pscustomobject]@{ ResponseUri = [uri]'http://example.invalid/file' })
        } 'non-HTTPS destination'
        Assert-HttpsResponse ([pscustomobject]@{ ResponseUri = [uri]'https://example.invalid/file' })
        $executable = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
        Assert-Throws {
            Invoke-NativeOutput $executable @('-NoProfile', '-Command', '[Console]::Error.WriteLine("fixture stderr"); exit 7')
        } 'exit 7[\s\S]*fixture stderr'
    }
    Test-Case 'Hardware prompt is allowlisted and unknown VRAM stays unknown' {
        $script:Questions.Clear()
        Set-Answers @()
        Write-HardwarePrompt
        $text = [IO.File]::ReadAllText((Get-PromptPath))
        Assert-True ((Get-PromptPath) -match 'Documents[/\\]Local-AI-Gemini-Prompt.txt$') 'Prompt is not in Documents'
        Assert-True ($text -match 'GPU memory: Unknown') 'Unknown VRAM not preserved'
        Assert-True ($text -notmatch 'DO-NOT-SHARE|AdapterRAM: 1') 'Hardware prompt leaked an excluded field'
        Assert-True ($text -match 'TOOL CALLING') 'No tool capability requirement'
        Assert-True ($text -match '(?m)^MODEL: .+$') 'Missing MODEL copy-back line'
        Assert-True ($text.Contains('only value the user must copy')) 'Prompt still asks for redundant values'
        Assert-True ($text -match 'before\r?\nany greeting') 'Copy-back block is not required first'
        Assert-True ($text.Contains('ONLY allowed Ollama Library recommendations')) 'Gemini is not restricted to the verified list'
        Assert-True ($text.Contains('llama3.1:8b') -and $text.Contains('mistral-nemo:12b')) 'Verified candidates missing from prompt'
        Assert-True ($text -notmatch 'qwen2\.5-coder') 'Known ineligible Qwen tag leaked into candidate prompt'
        Assert-True ($text.Contains('fixes agent context at 65536 tokens')) 'Fixed context is not explained'
        Assert-True ($text.Contains('up to 50% of installed RAM (16 GiB)')) 'Missing RAM-derived iGPU planning ceiling'
        Assert-True ($text.Contains('NOT a fixed reservation')) 'Shared memory described as fixed'
        Assert-True ($text.Contains('Do not infer a BIOS carve-out')) 'Missing anti-inference memory rule'
        Assert-True ($text -notmatch 'installing 96 GiB') 'Prompt contains stale hardcoded memory example'
        Assert-True ($text.Contains('Do not claim exact remaining/free RAM')) 'Missing free-memory uncertainty'
        Assert-True (@($script:Questions | Where-Object { $_ -match 'Optional GPU memory' }).Count -eq 0) 'Still asks for ambiguous shared VRAM'
        Assert-True ($text.Contains('MANDATORY FOR THIS SETUP')) 'Missing required context emphasis'
        Assert-True ($text.Contains('Do not lower context to make it fit')) 'Prompt permits lowering required context'
    }
    Test-Case 'Installed capacity is not confused with smaller Windows-visible memory' {
        $script:Mode = '96gb-installed'
        Set-Answers @()
        Write-HardwarePrompt
        $text = [IO.File]::ReadAllText((Get-PromptPath))
        Assert-True ($text.Contains('Installed physical RAM: 96 GiB')) 'Incorrect installed memory'
        Assert-True ($text.Contains('Windows-visible physical RAM: 32 GiB')) 'Visible memory not separately labelled'
        Assert-True ($text.Contains('up to 50% of installed RAM (48 GiB)')) 'Wrong installed-memory planning ceiling'
        Assert-True ($text.Contains('the cause is unknown here')) 'Memory discrepancy treated as known reservation'
        $script:Mode = 'missing-installed-ram'
        Assert-Throws { Write-HardwarePrompt } 'Installed RAM capacity could not be read'
        $script:Mode = 'ok'
    }
    Test-Case 'Ollama start is automatic' {
        $script:VersionAttempts = 0
        $script:StartedOllama = $false
        function Invoke-OllamaApi([string]$Endpoint, $Body = $null) {
            $script:VersionAttempts++
            if ($script:VersionAttempts -lt 3) { throw 'fixture unavailable' }
            return [pscustomobject]@{ version = 'fixture' }
        }
        function Start-Process { $script:StartedOllama = $true }
        function Start-Sleep {}
        Wait-Ollama
        Assert-True $script:StartedOllama 'Ollama was not started automatically'
        Assert-True ($script:VersionAttempts -eq 3) 'Ollama readiness was not retried'
    }
    Test-Case 'Model acquisition creates context-budget alias' {
        $script:Questions.Clear()
        Set-Answers @('model:q4', 'y')
        Select-LocalModel
        Assert-True ($script:LocalModel -eq 'local-ai-model-q4') 'Wrong alias'
        Assert-True ([IO.File]::ReadAllText((Join-Path $script:Work 'Modelfile')) -match 'num_ctx 65536') 'Missing context budget'
        Assert-True (@($script:Questions | Where-Object { $_ -match '^Model source:' }).Count -eq 0) 'Still asks the user to classify the source'
        $fields = @($script:Questions | Where-Object { $_ -match '^(MODEL|DOWNLOAD_GIB|CONTEXT_TOKENS):' } |
            ForEach-Object { ($_ -split ':')[0] })
        Assert-True (($fields -join ',') -eq 'MODEL') 'Wizard still asks for derivable model metadata'
    }
    Test-Case 'Compatible GGUF file path containing spaces' {
        $gguf = Join-Path $script:Fixture 'weights with spaces.gguf'
        [IO.File]::WriteAllText($gguf, 'GGUFfixture')
        Set-Answers @('https://huggingface.co/example/tool-model', $gguf, 'y')
        Select-LocalModel
        Assert-True ($script:LocalModel -eq 'local-ai-imported-weights-with-spaces') 'Import alias was not derived from the file'
    }
    Test-Case 'Invalid GGUF rejected' {
        $gguf = Join-Path $script:Fixture 'bad.gguf'
        [IO.File]::WriteAllText($gguf, 'invalid')
        Set-Answers @('https://huggingface.co/example/tool-model', $gguf)
        Assert-Throws { Select-LocalModel } 'not a GGUF'
        Set-Answers @('https://example.invalid/model.gguf')
        Assert-Throws { Select-LocalModel } 'Only huggingface.co URLs'
    }
    Test-Case 'Low disk rejected before pull' {
        $script:Mode = 'low-disk'
        $script:Calls.Clear()
        Set-Answers @('model')
        Assert-Throws { Select-LocalModel } 'Not enough'
        Assert-True (@($script:Calls | Where-Object { $_.Arguments[0] -eq 'pull' }).Count -eq 0) 'Downloaded despite low disk'
        $script:Mode = 'ok'
    }
    Test-Case 'Derivable model metadata is never requested' {
        $source = [IO.File]::ReadAllText($scriptPath)
        Assert-True ($source -notmatch "Read-Choice 'DOWNLOAD_GIB:|Read-Choice 'CONTEXT_TOKENS:") 'Still asks for size or context'
        Assert-True ($source -notmatch 'Copy the reviewed prompt to the clipboard|different model storage') 'Still asks for automatable clipboard or storage work'
    }
    Test-Case 'Required context and model limit enforced without misleading alias' {
        $script:Calls.Clear()
        Set-Answers @('NONE')
        Assert-Throws { Select-LocalModel } 'no suitable local model'
        Assert-True ($script:Calls.Count -eq 0) 'Invalid context triggered download'
        foreach ($mode in @('short-context', 'info-context')) {
            $script:Mode = $mode
            Set-Answers @('model')
            Assert-Throws { Select-LocalModel } '32768 context tokens'
        }
        Assert-True (@($script:Calls | Where-Object { $_.Arguments[0] -eq 'pull' }).Count -eq 0) 'Downloaded a known 32K model'
        Assert-True (@($script:Calls | Where-Object { $_.Arguments[0] -eq 'create' }).Count -eq 0) 'Created unsupported context alias'
        $script:Mode = 'unknown-context'
        Set-Answers @('model', 'y')
        Select-LocalModel
        Assert-True ($script:Context -eq 65536) 'Default context is not 65536'
        $script:Mode = 'ok'
    }
    Test-Case 'Out-of-catalog Gemini recommendation is rejected before pull' {
        $script:Calls.Clear()
        Set-Answers @('qwen2.5-coder:14b')
        Assert-Throws { Select-LocalModel } 'not in the wizard-verified candidate list'
        Assert-True (@($script:Calls | Where-Object { $_.Arguments[0] -eq 'pull' }).Count -eq 0) 'Pulled an out-of-catalog recommendation'
    }
    Test-Case 'Installed exact model is reused without pull' {
        $script:InstalledModel = 'model:q4'
        $script:Calls.Clear()
        Set-Answers @('model:q4', 'y')
        Select-LocalModel
        Assert-True (@($script:Calls | Where-Object { $_.Arguments[0] -eq 'pull' }).Count -eq 0) 'Installed model was pulled again'
        Assert-True (@($script:Calls | Where-Object { $_.Arguments[0] -eq 'create' }).Count -eq 1) 'Local context alias was not created'
        $script:InstalledModel = $null
    }
    Test-Case 'Remote and non-tool models rejected' {
        foreach ($mode in @('remote', 'no-tools')) {
            $script:Mode = $mode
            Set-Answers @('model', 'y')
            Assert-Throws { Select-LocalModel } 'cloud inference|tool calling'
        }
        $script:Mode = 'ok'
    }
    Test-Case 'Failed model download surfaces' {
        $script:Mode = 'pull-fails'
        Set-Answers @('model', 'y')
        Assert-Throws { Select-LocalModel } 'Download failed'
        $script:Mode = 'ok'
    }
    Test-Case 'Actual valid tool-call output checked' {
        Test-LocalModel
        Assert-True ($script:LastBody.options.num_ctx -eq $script:Context) 'Wrong inference context'
        Assert-True ($script:LastBody.tools[0].function.name -eq 'get_temperature') 'Wrong tool'
        Assert-True ($script:LastBody.messages[0].content -eq 'What is the temperature in New York?') 'Not using documented natural-language tool request'
    }
    Test-Case 'Absent wrong malformed and HTTP-error tool responses fail' {
        foreach ($mode in @('no-call', 'wrong-call', 'json-prose', 'malformed', 'http-error')) {
            $script:Mode = $mode
            Assert-Throws { Test-LocalModel } 'NOT verified|HTTP 500'
        }
        $script:Mode = 'ok'
    }
    Test-Case 'Unverified tools require explicit choice and never set passed' {
        $script:Mode = 'no-call'
        Set-Answers @('2')
        Confirm-LocalModelTools
        Assert-True (-not $script:ToolCheckPassed) 'Failed check was marked passed'
        Set-Answers @('q')
        Assert-Throws { Confirm-LocalModelTools } 'cancelled'
        $script:Mode = 'http-error'
        Set-Answers @('2')
        Assert-Throws { Confirm-LocalModelTools } 'HTTP 500'
        $script:Mode = 'ok'
        Confirm-LocalModelTools
        Assert-True $script:ToolCheckPassed 'Successful check not recorded'
        Assert-True ($script:LastBody.options.num_predict -eq 1024) 'Insufficient initial output budget'
    }
    Test-Case 'Reasoning output limit is explained and larger-budget retry can pass' {
        $script:Mode = 'length'
        Assert-Throws { Test-LocalModel } '1024-token output limit'
        Set-Answers @('1')
        Confirm-LocalModelTools
        Assert-True $script:ToolCheckPassed 'Retry did not pass'
        Assert-True ($script:LastBody.options.num_predict -eq 2048) 'Retry did not increase output budget'
        $script:Mode = 'ok'
    }
    Test-Case 'Profile scope is explicit for every code CLI operation' {
        $source = [IO.File]::ReadAllText($scriptPath)
        Assert-True ($source -notmatch "'--install-extension', 'GitHub.copilot-chat'") 'Tried to install bundled Copilot Chat from the marketplace'
        Assert-True ($source -match "'--profile', 'Local AI', '--install-extension', 'Ollama.ollama'") 'Ollama extension not profile-scoped'
        Assert-True ($source -match "'--new-window', '--profile', 'Local AI'\)") 'Profile creation launch is missing'
        Assert-True ($source -notmatch 'Full path to an existing locally checked-out repository') 'Still asks for a repository path'
        Assert-True ($source -notmatch 'Invoke-Expression|Set-ExecutionPolicy|AdapterRAM\s*=') 'Unexpected unsafe/global behavior'
        Assert-True ($source.Contains('checks the exact Ollama tag')) 'Missing exact-tag preflight explanation'
        Assert-True ($source.Contains('reused without pulling it again')) 'Missing installed-model reuse explanation'
        Assert-True ($source.Contains('[int]$response.StatusCode -ne 206')) 'Bounded registry range response is not enforced'
    }
    Test-Case 'VS Code profile readiness failure retains native diagnostics' {
        function Invoke-NativeOutput([string]$File, [string[]]$Arguments) {
            Assert-True ($Arguments -contains '--list-extensions') 'Unexpected captured-output command'
            throw "Command failed (exit 9): fixture-code --list-extensions`nfixture extension list failure"
        }
        Assert-Throws { Wait-CodeProfile 1 } 'did not make the Local AI profile available[\s\S]*exit 9[\s\S]*fixture extension list failure'
    }
    Test-Case 'Installer signature mismatch rejected' {
        function Get-AuthenticodeSignature { return [pscustomobject]@{ Status = 'Valid'; SignerCertificate = [pscustomobject]@{ Subject = 'Untrusted publisher' } } }
        Assert-Throws { Assert-SignedInstaller 'fixture.exe' 'Microsoft' } 'not valid'
    }
    Test-Case 'Existing application can be found outside PATH' {
        $file = Join-Path $script:Fixture 'application with spaces.cmd'
        [IO.File]::WriteAllText($file, 'fixture')
        $result = Get-Application 'no-such-local-ai-fixture.exe' @($file)
        Assert-True ($result -eq $file) 'Candidate path was not detected'
    }
    Test-Case 'Composer flow reuses apps and preserves profile settings on rerun' {
        $cli = Join-Path $script:Fixture 'fixture-code.ps1'
        [IO.File]::WriteAllText($cli, @'
if ($args -contains '--version') { Write-Output '1.127.0' }
if ($args -contains '--list-extensions') { Write-Output 'Ollama.ollama' }
$global:LASTEXITCODE = 0
'@)
        function Get-Code { return $cli }
        function Get-Ollama { return 'fixture-ollama' }
        function Wait-CodeProfile { return @() }
        $defaultSettings = Join-Path $script:Fixture 'default-settings.json'
        [IO.File]::WriteAllText($defaultSettings, '{"keep":"unchanged"}')
        $script:Calls.Clear()
        foreach ($run in @(1, 2)) {
            Install-Applications
            Wait-Ollama
            Set-Answers @('model:q4', 'y')
            Write-HardwarePrompt
            Select-LocalModel
            Test-LocalModel
            Connect-Code
        }
        Assert-True ([IO.File]::ReadAllText($defaultSettings) -eq '{"keep":"unchanged"}') 'Existing settings changed'
        $codeCalls = @($script:Calls | Where-Object { $_.File -eq $cli })
        Assert-True ($codeCalls.Count -eq 4) 'Missing extension/launch calls'
        foreach ($call in $codeCalls) {
            Assert-True ($call.Arguments -contains '--profile' -and $call.Arguments -contains 'Local AI') 'Unscoped profile operation'
        }
        Assert-True ($codeCalls[0].Arguments -contains '--new-window' -and $codeCalls[1].Arguments -contains '--install-extension') 'First run did not create the profile before extension installation'
        Assert-True ($codeCalls[2].Arguments -contains '--new-window' -and $codeCalls[3].Arguments -contains '--install-extension') 'Rerun did not open the profile before extension installation'
    }
    Test-Case 'Fresh installs wait only for installers and surface cancellation' {
        $cli = Join-Path $script:Fixture 'fresh-code.ps1'
        [IO.File]::WriteAllText($cli, "Write-Output '1.127.0'`n`$global:LASTEXITCODE = 0")
        $script:InstalledCode = $false
        $script:InstalledOllama = $false
        $script:InstallerExit = 0
        $script:InstallerWaits = 0
        $script:Signatures = [Collections.Generic.List[string]]::new()
        function Get-Code { if ($script:InstalledCode) { return $cli }; return $null }
        function Get-Ollama { if ($script:InstalledOllama) { return 'fixture-ollama' }; return $null }
        function Save-Download([string]$Uri, [string]$Destination) {
            Assert-True ($Uri -match '^https://(update.code.visualstudio.com|ollama.com)/') 'Unexpected installer source'
            [IO.File]::WriteAllText($Destination, 'fixture')
        }
        function Assert-SignedInstaller([string]$Path, [string]$Publisher) { $script:Signatures.Add($Publisher) }
        function Start-Process {
            param([string]$FilePath, [switch]$PassThru)
            $process = [pscustomobject]@{ Installer = $FilePath; ExitCode = $script:InstallerExit }
            $process | Add-Member -MemberType ScriptMethod -Name WaitForExit -Value {
                $script:InstallerWaits++
                if ($this.Installer -match 'VSCode') { $script:InstalledCode = $true }
                else { $script:InstalledOllama = $true }
            }
            return $process
        }
        Set-Answers @('y', 'y')
        Install-Applications
        Assert-True ($script:InstallerWaits -eq 2) 'Did not wait for both installer roots'
        Assert-True ($script:Signatures -contains 'Microsoft' -and $script:Signatures -contains 'Ollama') 'Missing publisher checks'
        $script:InstalledCode = $false
        $script:InstallerExit = 1602
        Set-Answers @('y')
        Assert-Throws { Install-Applications } 'installer exited 1602'
    }
    Write-Host "`n$script:Passed mocked tests passed. This is not real Windows installation/GPU/VS Code validation."
} finally {
    Remove-Item -LiteralPath $script:Fixture -Recurse -Force
}
