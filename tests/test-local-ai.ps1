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
$script:Context = 8192
$script:LocalModel = 'fixture-model'
$script:Answers = [Collections.Generic.Queue[string]]::new()
$script:Calls = [Collections.Generic.List[object]]::new()
$script:Mode = 'ok'
$script:ToolCheckPassed = $false
$script:PromptFolder = Join-Path $script:Fixture 'Documents'
function Get-PromptPath {
    [void][IO.Directory]::CreateDirectory($script:PromptFolder)
    return Join-Path $script:PromptFolder 'Local-AI-Gemini-Prompt.txt'
}

function Read-Choice([string]$Message) {
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
    if ($script:Mode -eq 'pull-fails' -and $Arguments[0] -eq 'pull') { throw 'Download failed (fixture).' }
}
function Invoke-OllamaApi([string]$Endpoint, $Body = $null) {
    if ($script:Mode -eq 'http-error') { throw 'HTTP 500 (fixture)' }
    if ($Endpoint -eq 'show') {
        if ($script:Mode -eq 'remote') { return [pscustomobject]@{ remote_host = 'https://example.invalid'; capabilities = @('tools') } }
        if ($script:Mode -eq 'no-tools') { return [pscustomobject]@{ capabilities = @('completion') } }
        return [pscustomobject]@{ capabilities = @('completion', 'tools') }
    }
    if ($Endpoint -eq 'chat') {
        $script:LastBody = $Body
        if ($script:Mode -eq 'length' -and $Body.options.num_predict -lt 2048) {
            return [pscustomobject]@{ done = $true; done_reason = 'length'; message = [pscustomobject]@{ thinking = 'fixture reasoning' } }
        }
        if ($script:Mode -eq 'no-call') { return [pscustomobject]@{ done = $true; message = [pscustomobject]@{ content = 'hello' } } }
        if ($script:Mode -eq 'malformed') { return [pscustomobject]@{ unexpected = 1 } }
        $value = if ($script:Mode -eq 'wrong-call') { 'wrong' } else { 'ready' }
        return [pscustomobject]@{ done = $true; message = [pscustomobject]@{ tool_calls = @(
            [pscustomobject]@{ function = [pscustomobject]@{ name = 'local_ai_check'; arguments = [pscustomobject]@{ value = $value } } }
        ) } }
    }
    return [pscustomobject]@{ version = 'fixture' }
}
function Get-ModelVolume {
    $space = if ($script:Mode -eq 'low-disk') { 1GB } else { 100GB }
    return [pscustomobject]@{ AvailableFreeSpace = $space }
}
function Get-CimInstance([string]$ClassName) {
    switch ($ClassName) {
        'Win32_OperatingSystem' { return [pscustomobject]@{ Caption = 'Fixture Windows'; Version = '10.0.22631'; CSName = 'DO-NOT-SHARE-HOST' } }
        'Win32_Processor' { return [pscustomobject]@{ Name = 'Fixture CPU'; ProcessorId = 'DO-NOT-SHARE-SERIAL' } }
        'Win32_ComputerSystem' { return [pscustomobject]@{ TotalPhysicalMemory = 32GB; UserName = 'DO-NOT-SHARE-USER' } }
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
    Test-Case 'Cancelled prompt is explicit' {
        Set-Answers @('q')
        Assert-Throws { Read-Choice 'fixture' } 'cancelled'
    }
    Test-Case 'Hardware prompt is allowlisted and unknown VRAM stays unknown' {
        Set-Answers @('', 'n', '')
        Write-HardwarePrompt
        $text = [IO.File]::ReadAllText((Get-PromptPath))
        Assert-True ((Get-PromptPath) -match 'Documents[/\\]Local-AI-Gemini-Prompt.txt$') 'Prompt is not in Documents'
        Assert-True ($text -match 'GPU memory: Unknown') 'Unknown VRAM not preserved'
        Assert-True ($text -notmatch 'DO-NOT-SHARE|AdapterRAM: 1') 'Hardware prompt leaked an excluded field'
        Assert-True ($text -match 'TOOL CALLING') 'No tool capability requirement'
        Assert-True ($text -match '(?m)^MODEL: .+\r?\nCONTEXT_TOKENS: .+\r?\nDOWNLOAD_GIB: ') 'Missing ordered copy-back block'
        Assert-True ($text -match 'before\r?\nany greeting') 'Copy-back block is not required first'
        Assert-True ($text -match 'structured Ollama API tool_calls') 'Missing structured tool-call evidence requirement'
    }
    Test-Case 'Model acquisition creates context-budget alias' {
        Set-Answers @('1', 'model:q4', '4', '16384', 'y')
        Select-LocalModel
        Assert-True ($script:LocalModel -eq 'local-ai-model-q4') 'Wrong alias'
        Assert-True ([IO.File]::ReadAllText((Join-Path $script:Work 'Modelfile')) -match 'num_ctx 16384') 'Missing context budget'
    }
    Test-Case 'Compatible GGUF file path containing spaces' {
        $gguf = Join-Path $script:Fixture 'weights with spaces.gguf'
        [IO.File]::WriteAllText($gguf, 'GGUFfixture')
        Set-Answers @('2', $gguf, 'imported', '4', '', 'y')
        Select-LocalModel
        Assert-True ($script:LocalModel -eq 'local-ai-imported') 'Import alias missing'
    }
    Test-Case 'Invalid GGUF rejected' {
        $gguf = Join-Path $script:Fixture 'bad.gguf'
        [IO.File]::WriteAllText($gguf, 'invalid')
        Set-Answers @('2', $gguf)
        Assert-Throws { Select-LocalModel } 'not a GGUF'
    }
    Test-Case 'Low disk rejected before pull' {
        $script:Mode = 'low-disk'
        $script:Calls.Clear()
        Set-Answers @('1', 'model', '4')
        Assert-Throws { Select-LocalModel } 'Not enough'
        Assert-True ($script:Calls.Count -eq 0) 'Downloaded despite low disk'
        $script:Mode = 'ok'
    }
    Test-Case 'Invalid size/context rejected before pull' {
        foreach ($answers in @(@('1', 'model', 'bad'), @('1', 'model', '1', '0'), @('1', 'model', '1', '9999999999'))) {
            Set-Answers $answers
            Assert-Throws { Select-LocalModel } 'numeric|integer'
        }
    }
    Test-Case 'Remote and non-tool models rejected' {
        foreach ($mode in @('remote', 'no-tools')) {
            $script:Mode = $mode
            Set-Answers @('1', 'model', '1', '', 'y')
            Assert-Throws { Select-LocalModel } 'cloud inference|tool calling'
        }
        $script:Mode = 'ok'
    }
    Test-Case 'Failed model download surfaces' {
        $script:Mode = 'pull-fails'
        Set-Answers @('1', 'model', '1', '', 'y')
        Assert-Throws { Select-LocalModel } 'Download failed'
        $script:Mode = 'ok'
    }
    Test-Case 'Actual valid tool-call output checked' {
        Test-LocalModel
        Assert-True ($script:LastBody.options.num_ctx -eq $script:Context) 'Wrong inference context'
        Assert-True ($script:LastBody.tools[0].function.name -eq 'local_ai_check') 'Wrong tool'
    }
    Test-Case 'Absent wrong malformed and HTTP-error tool responses fail' {
        foreach ($mode in @('no-call', 'wrong-call', 'malformed', 'http-error')) {
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
        Assert-True ($source -match "'--profile', 'Local AI', '--install-extension', 'GitHub.copilot-chat'") 'Chat extension not profile-scoped'
        Assert-True ($source -match "'--profile', 'Local AI', '--install-extension', 'Ollama.ollama'") 'Ollama extension not profile-scoped'
        Assert-True ($source -match "'--new-window', '--profile', 'Local AI', \`$repo") 'Repository launch not profile-scoped'
        Assert-True ($source -notmatch 'Invoke-Expression|Set-ExecutionPolicy|AdapterRAM\s*=') 'Unexpected unsafe/global behavior'
        Assert-True ($source.Contains('Click the model selector')) 'Missing Desktop model-selector instruction'
        Assert-True ($source.Contains('type "Say hello" and press Enter')) 'Missing simple first prompt'
        Assert-True ($source.Contains('Return here and enter that same model name')) 'Missing return-to-wizard instruction'
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
if ($args -contains '--list-extensions') { Write-Output 'GitHub.copilot-chat'; Write-Output 'Ollama.ollama' }
$global:LASTEXITCODE = 0
'@)
        function Get-Code { return $cli }
        function Get-Ollama { return 'fixture-ollama' }
        function Get-Command {
            param($Name, $CommandType, $ErrorAction)
            if ($Name -eq 'git.exe') { return [pscustomobject]@{ Source = 'fixture-git' } }
            return Microsoft.PowerShell.Core\Get-Command @PSBoundParameters
        }
        $defaultSettings = Join-Path $script:Fixture 'default-settings.json'
        [IO.File]::WriteAllText($defaultSettings, '{"keep":"unchanged"}')
        $script:Calls.Clear()
        foreach ($run in @(1, 2)) {
            Install-Applications
            Wait-Ollama
            Set-Answers @('', 'n', '', '1', 'model:q4', '4', '8192', 'y', 'y', $script:Fixture)
            Write-HardwarePrompt
            Select-LocalModel
            Test-LocalModel
            Connect-Code
        }
        Assert-True ([IO.File]::ReadAllText($defaultSettings) -eq '{"keep":"unchanged"}') 'Existing settings changed'
        $codeCalls = @($script:Calls | Where-Object { $_.File -eq $cli })
        Assert-True ($codeCalls.Count -eq 6) 'Missing extension/launch calls'
        foreach ($call in $codeCalls) {
            Assert-True ($call.Arguments -contains '--profile' -and $call.Arguments -contains 'Local AI') 'Unscoped profile operation'
        }
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
