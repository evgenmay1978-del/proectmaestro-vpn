param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Exit-Failure([string]$Code) {
    [Console]::Error.WriteLine("release_validation_failed code=$Code")
    exit 1
}

function Test-AmbiguousRootedPath([string]$Path) {
    if (-not [IO.Path]::IsPathRooted($Path)) {
        return $false
    }
    if ([IO.Path]::DirectorySeparatorChar -ne '\') {
        return $false
    }
    $root = [IO.Path]::GetPathRoot($Path)
    if ([string]::IsNullOrEmpty($root)) {
        return $true
    }
    return $root.Length -eq 1 -or ($root.Length -eq 2 -and $root[1] -eq ':')
}

$releaseDir = $null
$evidenceTrust = $null
$goBinary = $null
$seenRelease = $false
$seenTrust = $false
$seenGo = $false
$validationPackages = @(
    './internal/api'
    './internal/controlplane'
    './internal/subgen'
    './internal/whitelistbalance'
    './internal/shadowbilling'
    './internal/whitelistapi/v1'
    './internal/release'
    './internal/whitelistready'
    './internal/canary'
    './cmd/maestro-release-validate'
    './cmd/maestro-whitelist-ready'
    './cmd/maestro-xray-cdn-canary'
    './internal/testsupport/whitelistfixture'
)
$commercialPythonSources = @(Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot '..\deploy') -Filter 'vpn_bot_maestro_*.py' -File -ErrorAction SilentlyContinue)

for ($index = 0; $index -lt $args.Count; $index += 2) {
    if ($index + 1 -ge $args.Count) {
        Exit-Failure 'arguments_invalid'
    }
    $name = [string]$args[$index]
    $value = [string]$args[$index + 1]
    switch ($name) {
        '--release-dir' {
            if ($seenRelease) { Exit-Failure 'arguments_invalid' }
            $releaseDir = $value
            $seenRelease = $true
        }
        '--evidence-trust' {
            if ($seenTrust) { Exit-Failure 'arguments_invalid' }
            $evidenceTrust = $value
            $seenTrust = $true
        }
        '--go-binary' {
            if ($seenGo) { Exit-Failure 'arguments_invalid' }
            $goBinary = $value
            $seenGo = $true
        }
        default {
            Exit-Failure 'arguments_invalid'
        }
    }
}

if (-not $seenRelease -or -not $seenTrust -or
    [string]::IsNullOrWhiteSpace($releaseDir) -or [string]::IsNullOrWhiteSpace($evidenceTrust)) {
    Exit-Failure 'arguments_invalid'
}

$callerDirectory = (Get-Location).ProviderPath
try {
    if ((Test-AmbiguousRootedPath $releaseDir) -or
        (Test-AmbiguousRootedPath $evidenceTrust) -or
        (-not [string]::IsNullOrWhiteSpace($goBinary) -and (Test-AmbiguousRootedPath $goBinary))) {
        Exit-Failure 'arguments_invalid'
    }
    if (-not [IO.Path]::IsPathRooted($releaseDir)) {
        $releaseDir = [IO.Path]::GetFullPath((Join-Path $callerDirectory $releaseDir))
    }
    if (-not [IO.Path]::IsPathRooted($evidenceTrust)) {
        $evidenceTrust = [IO.Path]::GetFullPath((Join-Path $callerDirectory $evidenceTrust))
    }

    if ([string]::IsNullOrWhiteSpace($goBinary)) {
        $goBinary = (Get-Command -Name 'go' -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
    } elseif ([IO.Path]::IsPathRooted($goBinary)) {
        if (-not (Test-Path -LiteralPath $goBinary -PathType Leaf)) {
            Exit-Failure 'go_not_found'
        }
    } else {
        $candidate = [IO.Path]::GetFullPath((Join-Path $callerDirectory $goBinary))
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            $goBinary = $candidate
        } else {
            $goBinary = (Get-Command -Name $goBinary -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
        }
    }

    $repoRoot = Split-Path -Parent $PSScriptRoot
    $backend = Join-Path $repoRoot 'backend'
    Push-Location -LiteralPath $backend
    try {
        $existingPackages = @(
            $validationPackages | Where-Object {
                Test-Path -LiteralPath (Join-Path $backend $_.Substring(2)) -PathType Container
            }
        )
        $testArgs = @('test', '-count=1') + $existingPackages
        & $goBinary @testArgs
        if ($LASTEXITCODE -ne 0) {
            Exit-Failure 'go_tests_failed'
        }
        if ($commercialPythonSources.Count -gt 0) {
            $python = (Get-Command -Name 'python' -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source
            & $python -X utf8 -m py_compile $commercialPythonSources.FullName
            if ($LASTEXITCODE -ne 0) {
                Exit-Failure 'python_compile_failed'
            }
        }
        $validatorArgs = @(
            'run',
            './cmd/maestro-release-validate',
            '--release-dir',
            $releaseDir,
            '--evidence-trust',
            $evidenceTrust
        )
        & $goBinary @validatorArgs
        $exitCode = $LASTEXITCODE
    } finally {
        Pop-Location
    }
} catch {
    [Console]::Error.WriteLine('release_validation_failed code=wrapper_failed')
    exit 1
}

exit $exitCode
