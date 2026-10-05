<#
.SYNOPSIS
    swagCore — установщик ноды для Windows. Одна команда, полная настройка.

.DESCRIPTION
    Скрипт самодостаточен: скачивает агента с сервера платформы, кладёт в
    C:\ProgramData\swagcore, регистрирует его как СЛУЖБУ Windows (автозапуск +
    автоперезапуск при падении), запускает и проверяет подключение к панели.

    Никаких зависимостей: чистая Windows 10/11 PowerShell 5.1 и новее.

.EXAMPLE
    # Обычный случай — токен из панели «Добавить ноду»:
    irm https://core.swag.best/download/install.ps1 | iex

.EXAMPLE
    # Явный вызов с параметрами:
    .\install.ps1 -Token "abc123..." -Name "office-pc" -MaxMemMB 2048

.PARAMETER Token
    Токен ноды из панели swagCore. Обязателен.

.PARAMETER Name
    Понятное имя машины (для себя). Не влияет на hostname в системе.

.PARAMETER MaxMemMB
    Лимит RAM, который платформа может занять на этой машине (МБ). 0 = без лимита.

.PARAMETER MaxDiskGB
    Лимит диска для проектов платформы (ГБ). 0 = без лимита.

.PARAMETER NoDocker
    Считать, что Docker не используется (только process-режим).

.PARAMETER Unattended
    Не задавать вопросов, всё автоматически (по умолчанию $true).

.PARAMETER Elevated
    Внутренний флаг: ставится при перезапуске с правами администратора.
    Вручную указывать не нужно.
#>
[CmdletBinding()]
param(
    [string]$Token,
    [string]$Name = "",
    [int]$MaxMemMB = 0,
    [int]$MaxDiskGB = 0,
    [switch]$NoDocker,
    [switch]$Uninstall,
    [switch]$Elevated,
    [bool]$Unattended = $true
)

# v0.7.0: снимаем ExecutionPolicy для ТЕКУЩЕГО процесса.
# На машине с Restricted/RemoteSigned всё, что ниже, упало бы с
# «выполнение сценариев отключено» — в том числе повышение прав.
try {
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force -ErrorAction SilentlyContinue
} catch { }
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# ---------- константы ----------
$ServiceName  = "swagcore-agent"
$DataDir      = "C:\ProgramData\swagcore"
$AgentPath    = Join-Path $DataDir "swagcore-agent.exe"
$LogPath      = Join-Path $DataDir "install.log"
$CoreHost     = "core.swag.best"
$DownloadBase = "https://$CoreHost/download"

# ---------- вывод ----------
$script:StepNo = 0
function Write-Step([string]$Text) {
    $script:StepNo++
    Write-Host ""
    Write-Host "[$script:StepNo] $Text" -ForegroundColor Cyan
}
function Write-Ok([string]$Text)    { Write-Host "    OK  $Text" -ForegroundColor Green }
function Write-Warn([string]$Text)  { Write-Host "    !   $Text" -ForegroundColor Yellow }
function Write-Err([string]$Text)   { Write-Host "    x   $Text" -ForegroundColor Red }
function Log([string]$Text) {
    try {
        if (-not (Test-Path $DataDir)) { New-Item -ItemType Directory -Path $DataDir -Force | Out-Null }
        Add-Content -Path $LogPath -Value ("[{0}] {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $Text)
    } catch { }
}

# ---------- повышение прав до администратора ----------
function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    return (New-Object Security.Principal.WindowsPrincipal $id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-Elevated {
    # Перезапускает скрипт с теми же аргументами в elevated-процессе.
    # Возвращает $true, если права получены (установка продолжится).
    $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$($PSCommandPath)`"")
    # Передаём параметры ЯВНО. Раньше тут был обход $PSBoundParameters,
    # который вместе с параметрами CmdletBinding и [bool]$Unattended
    # подставлял в повышенный процесс значения по умолчанию — токен
    # терялся, и elevated-установка ставила агента БЕЗ токена.
    if ($Token)      { $argList += @("-Token", "`"$($Token.Replace('`',''))`"") }
    if ($Name)       { $argList += @("-Name", "`"$($Name.Replace('`',''))`"") }
    if ($MaxMemMB -gt 0)  { $argList += @("-MaxMemMB", "$MaxMemMB") }
    if ($MaxDiskGB -gt 0) { $argList += @("-MaxDiskGB", "$MaxDiskGB") }
    if ($NoDocker)   { $argList += "-NoDocker" }
    $argList += "-Elevated"
    $argString = ($argList | ForEach-Object { if ($_ -match '\s') { '"' + $_ + '"' } else { $_ } }) -join ' '
    Log "elevating: $argString"
    try {
        Start-Process -FilePath "powershell.exe" -Verb RunAs -ArgumentList $argString -Wait -NoNewWindow -PassThru
        return $true
    } catch {
        Log "elevation refused: $($_.Exception.Message)"
        return $false
    }
}

# ---------- выбор режима установки ----------
# Служба Windows (нужны права администратора) — правильный вариант:
# автозапуск, автоперезапуск, переживает выход из сессии.
# Если прав нет и повышение не удалось — ставим в автозагрузку текущего
# пользователя (HKCU\Run). Тоже работает, просто без защиты от сбоев.
function Setup-Service {
    param([string]$BinPath)
    & sc.exe create $ServiceName binPath= $BinPath start= auto DisplayName= "swagCore Agent" 2>&1 | Out-Null
    Start-Sleep -Seconds 1
    return [bool](Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)
}

function Setup-RegistryAutostart {
    param([string]$BinPath)
    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    # убираем возможные старые дубли (v0.5.x-способ), чтобы не было гонки нод
    foreach ($legacy in @("swagcore-agent", "SwagCoreAgent")) {
        if ((Get-ItemProperty -Path $runKey -Name $legacy -ErrorAction SilentlyContinue)) {
            Remove-ItemProperty -Path $runKey -Name $legacy -ErrorAction SilentlyContinue
            Log "removed legacy autostart entry $legacy"
        }
    }
    Set-ItemProperty -Path $runKey -Name "swagcore-agent" -Value $BinPath -Force
    return $true
}

# ---------- удаление ----------
function Uninstall-Agent {
    Write-Step "Удаление агента"
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        Write-Host "    Останавливаем и удаляем службу..."
        & sc.exe stop  $ServiceName 2>&1 | Out-Null
        & sc.exe delete $ServiceName 2>&1 | Out-Null
    }
    # чистим старые записи автозагрузки из реестра (v0.5.x-способ)
    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    foreach ($legacy in @("swagcore-agent", "SwagCoreAgent")) {
        if ((Get-ItemProperty -Path $runKey -Name $legacy -ErrorAction SilentlyContinue)) {
            Remove-ItemProperty -Path $runKey -Name $legacy -ErrorAction SilentlyContinue
            Write-Ok "убрана старая запись автозагрузки $legacy"
        }
    }
    Get-Process swagcore-agent -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Write-Ok "агент удалён (каталог $DataDir оставлен — Projects/Sites в нём сохранились)"
}

# ---------- скачивание ----------
function Get-File($Url, $OutFile, [int]$Tries = 4) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    for ($i = 1; $i -le $Tries; $i++) {
        try {
            Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing -TimeoutSec 300
            return
        } catch {
            Write-Warn "попытка $i/$Tries не удалась: $($_.Exception.Message)"
            if ($i -lt $Tries) { Start-Sleep -Seconds (2 * $i) }
        }
    }
    throw "не удалось скачать $Url"
}

# ---------- проверка версии/сборки ----------
function Get-ServerBuild {
    # v0.7.0: .Content у Invoke-WebRequest — это byte[], а не string.
    # Раньше .Content.Trim() падал в catch и сборка сервера всегда
    # показывалась пустой ("сборка на сервере: ").
    try {
        $raw = (Invoke-WebRequest -Uri "$DownloadBase/agent/version.txt" -UseBasicParsing -TimeoutSec 20).Content
        if ($raw -is [byte[]]) { return [Text.Encoding]::ASCII.GetString($raw).Trim() }
        return ([string]$raw).Trim()
    } catch {
        return ""
    }
}

# =====================================================================
# MAIN
# =====================================================================

if ($Uninstall) { Uninstall-Agent; exit 0 }

# ---- 0. Права и каталог данных ----
# v0.6.0: раньше скрипт требовал администратора и без прав просто просил повышение.
# Теперь есть два режима и оба рабочие:
#   есть права  → агент ставится СЛУЖБЕЙ (автозапуск + перезапуск при падении);
#   нет прав    → пробуем повысить; если не вышло, ставим в автозагрузку (HKCU\Run)
#                 и складываем данные в профиль пользователя.
Write-Step "Проверяем права"
$IsAdmin = Test-Admin
$DataDirLocal = (Join-Path $env:LOCALAPPDATA "swagcore")
$RunAsUser = $false

if ($IsAdmin) {
    Write-Ok "запущено с правами администратора — ставим службу Windows"
} else {
    Write-Host "    !   нет прав администратора, пробуем запросить повышение…"
    if (-not $Elevated) {
        if (Invoke-Elevated -Token $Token -Name $Name -MaxMemMB $MaxMemMB -MaxDiskGB $MaxDiskGB -Unattended $Unattended) {
            Write-Ok "установка завершена в режиме с правами администратора"
            exit 0
        }
    }
    Write-Warn "продолжаем без прав администратора: агент встанет в автозагрузку при входе"
    $RunAsUser = $true
    $DataDir = $DataDirLocal
}
$AgentPath = Join-Path $DataDir "swagcore-agent.exe"
$LogPath   = Join-Path $DataDir "install.log"
Write-Ok "каталог данных: $DataDir"

# ---- 1. Токен ----
if (-not $Token) {
    Write-Host ""
    Write-Host "  swagCore — подключение новой ноды" -ForegroundColor White
    Write-Host "  --------------------------------" -ForegroundColor White
    Write-Host "  Вставьте токен из панели: https://$CoreHost/ -> «Добавить ноду»"
    Write-Host ""
    $Token = Read-Host "Вставьте токен"
}
$Token = $Token.Trim()
if ($Token -notmatch '^[A-Za-z0-9_\-]{16,128}$') {
    Write-Err "токен выглядит некорректно (ожидается 16-128 символов A-Z a-z 0-9 _ -)"
    exit 1
}

if (-not $Name) { $Name = $env:COMPUTERNAME }
$Name = ($Name -replace '[^A-Za-z0-9_.-]', '_')

if (-not (Test-Path $DataDir)) { New-Item -ItemType Directory -Path $DataDir -Force | Out-Null }
Log "=== install start: name=$Name mode=$(if($RunAsUser){'user'}else{'service'}) token=…$($Token.Substring($Token.Length-6)) ==="

# ---- 1. Проверка версии Windows и архитектуры ----
Write-Step "Проверяем систему"
$os = Get-CimInstance Win32_OperatingSystem
Write-Ok "Windows $($os.Caption) (сборка $($os.BuildNumber))"
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne "AMD64") {
    Write-Err "нужна 64-разрядная Windows (x64). Сейчас: $arch"
    exit 1
}
Write-Ok "архитектура x64"

# ---- 2. Свободное место ----
$freeGB = [math]::Round((Get-PSDrive C).Free / 1GB, 1)
Write-Ok "свободно на C: $freeGB ГБ"
if ($freeGB -lt 2) { Write-Warn "мало места (<2 ГБ) — установка может не удаться" }

# ---- 3. Остановка старого агента ----
Write-Step "Останавливаем прежнюю версию (если есть)"
Get-Process swagcore-agent -ErrorAction SilentlyContinue | ForEach-Object {
    Write-Host "    останавливаем процесс PID $($_.Id)…"
    Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
}
if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
    & sc.exe stop $ServiceName 2>&1 | Out-Null
    Start-Sleep -Seconds 1
}
# v0.6.0: снимаем ВСЕ старые записи автозагрузки. В v0.5.x их было две
# (swagcore-agent и SwagCoreAgent) с разными токенами — платформа создавала
# две ноды на одной машине, и приложения убивали друг друга за порт.
$legacyRun = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
$removed = 0
foreach ($legacy in @("swagcore-agent", "SwagCoreAgent")) {
    if ((Get-ItemProperty -Path $legacyRun -Name $legacy -ErrorAction SilentlyContinue)) {
        Remove-ItemProperty -Path $legacyRun -Name $legacy -ErrorAction SilentlyContinue
        $removed++
    }
}
if ($removed -gt 0) { Write-Ok "убрано старых записей автозагрузки: $removed" }
Write-Ok "готово"

# ---- 4. Скачивание агента ----
Write-Step "Скачиваем агента"
$remoteBuild = Get-ServerBuild
Write-Host "    сборка на сервере: $remoteBuild"
$tmp = Join-Path $env:TEMP "swagcore-agent.exe"
$url = "$DownloadBase/agent/windows/amd64/swagcore-agent.exe"
Get-File $url $tmp

# проверка контрольной суммы
# ВНИМАНИЕ: (Invoke-WebRequest).Content в PowerShell 5.1 для текстовых
# ответов отдаёт byte[], а не string — поэтому приводим явно, иначе .Trim()
# падает на «не найден метод Trim для System.Byte[]» и проверка молча
# пропускается (в v0.6.0 первая версия скрипта так и не смогла сверить чексумму).
try {
    $shaResp = Invoke-WebRequest -Uri "$url.sha256" -UseBasicParsing -TimeoutSec 30
    # ВАЖНО: тип проверяем ДО приведения. [string]byte[] даёт "System.Byte[]",
    # после чего проверка -is [byte[]] уже ложна и сверка пропускается.
    $raw = $shaResp.Content
    if ($raw -is [byte[]]) {
        $shaTxt = [Text.Encoding]::ASCII.GetString($raw)
    } elseif ($raw -is [string]) {
        $shaTxt = $raw
    } else {
        $shaTxt = [string]$raw
    }
    $shaTxt = $shaTxt.Trim()
    $want = ($shaTxt -split '\s+')[0].Trim().ToLower()
    if ($want -notmatch '^[0-9a-f]{64}$') {
        Write-Warn "не удалось разобрать .sha256 (первые 80 символов: '$($shaTxt.Substring(0, [Math]::Min(80, $shaTxt.Length)))')"
    }
    if ($want -notmatch '^[0-9a-f]{64}$') {
        Write-Warn "манифест sha256 нечитаем (получено: '$shaTxt') — пропускаем сверку"
    } else {
        $got = (Get-FileHash -Path $tmp -Algorithm SHA256).Hash.ToLower()
        if ($got -ne $want) {
            Remove-Item $tmp -Force -ErrorAction SilentlyContinue
            Write-Err "контрольная сумма не совпала: скачано $got, ожидалось $want — файл удалён, загрузка отменена"
            exit 1
        }
        Write-Ok "контрольная сумма сходится"
    }
} catch {
    Write-Warn "не удалось проверить контрольную сумму: $($_.Exception.Message)"
}

# ---- 5. Установка бинарника ----
Write-Step "Устанавливаем в $AgentPath"
if (Test-Path $AgentPath) {
    Copy-Item $AgentPath (Join-Path $DataDir "swagcore-agent.exe.old") -Force -ErrorAction SilentlyContinue
    Write-Ok "предыдущая сборка сохранена как swagcore-agent.exe.old"
}
Move-Item $tmp $AgentPath -Force
(Get-Item $AgentPath).LastWriteTime = Get-Date   # чтобы systemd-подобная логика "новее" работала
Write-Ok "установлено, $((Get-Item $AgentPath).Length) байт"

# v0.7.0: НИКОГДА не вызывай нативную команду при $ErrorActionPreference="Stop",
# если пишешь её вывод прямо в переменную. В PowerShell 5.1 запись в stderr
# нативной команды превращается в NativeCommandError и becomes терминирующей
# ошибкой — установка падала ровно на этом шаге. Redirected через cmd /c,
# stderr уходит в файл и не может ничего сломать.
$__verTmp = Join-Path $env:TEMP "swagcore-ver.txt"
$__verErr = Join-Path $env:TEMP "swagcore-ver.err"
cmd /c "`"$AgentPath`" version > `"$__verTmp`" 2> `"$__verErr`""
$localBuild = (Get-Content $__verTmp -ErrorAction SilentlyContinue | Select-Object -First 1)
if (-not $localBuild) {
    $localBuild = "unknown"
    Write-Warn "не удалось определить сборку агента (см. $__verErr)"
}
Write-Ok "локальная сборка: $localBuild"

# ---- 6. Автозапуск: служба или реестр ----
Write-Step "Настраиваем автозапуск"

$agentArgs = @(
    # БЕЗ "run": подкоманда "run" заставляла агента уйти в
    # foreground и НЕ вызвать StartServiceCtrlDispatcher. SCM без
    # рукопожатия рвал службу по таймауту (Event 7009) — после
    # перезагрузки ПК агент не поднимался.
    "--server", "wss://$CoreHost/agent",
    "--token", $Token,
    "--data", $DataDir
)
if ($MaxMemMB -gt 0) { $agentArgs += @("--max-mem", "$MaxMemMB") }
if ($MaxDiskGB -gt 0) { $agentArgs += @("--max-disk", "$MaxDiskGB") }
if ($NoDocker)       { $agentArgs += "--no-docker" }
if ($Name -and $Name.Trim() -ne "") { $agentArgs += @("--name", $Name) }

# sc.exe требует binPath с кавычками вокруг пути (в нём могут быть пробелы)
$binPath = '"' + $AgentPath + '" ' + ($agentArgs -join ' ')

$asService = $false
if (Test-Admin) {
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        & sc.exe delete $ServiceName 2>&1 | Out-Null
        Start-Sleep -Seconds 1
    }
    Setup-Service -BinPath $binPath | Out-Null
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        # автоперезапуск 3 раза при падении
        # v0.7.0: раньше здесь стояли всего 3 попытки и reset= 86400 (сутки).
        # После трёх сбоев за сутки SCM навсегда переставал поднимать
        # службу — агент лежал до перезагрузки ПК (случилось на ноде dream).
        # Теперь 6 попыток с растущими паузами и сброс счётчика каждые
        # 10 минут: серия сбоев не исчерпывает бюджет восстановления.
        & sc.exe failure $ServiceName reset= 600 `
              actions= restart/5000/restart/10000/restart/30000/restart/60000/restart/120000/restart/600000 2>&1 | Out-Null
        & sc.exe failureflag $ServiceName 1 2>&1 | Out-Null
        & sc.exe description $ServiceName "swagCore node agent — connects to $CoreHost and runs assigned projects" 2>&1 | Out-Null
        $asService = $true
        Write-Ok "служба Windows $ServiceName создана (автозапуск + перезапуск при падении x3)"
    } else {
        Write-Warn "не удалось создать службу, переключаюсь на автозагрузку"
    }
}
if (-not $asService) {
    Setup-RegistryAutostart -BinPath $binPath | Out-Null
    Write-Ok "автозагрузка при входе в Windows (HKCU\Run)"
    if (-not (Test-Admin)) {
        Write-Warn "без прав администратора агент не переживёт выход из сессии и не перезапустится при падении"
        Write-Warn "чтобы получить полноценную службу, запустите ту же команду в PowerShell от имени администратора"
    }
}

# ---- 7. Запуск ----
Write-Step "Запускаем агента"
$started = $false
if ($asService) {
    & sc.exe start $ServiceName 2>&1 | Out-Null
    Start-Sleep -Seconds 4
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -eq "Running") {
        $pid_ = (Get-CimInstance Win32_Service -Filter "Name='$ServiceName'").ProcessId
        Write-Ok "служба работает (PID $pid_)"
        $started = $true
    }
}
if (-not $started) {
    # фоновый запуск без окна — консольное окно не мигает
    Start-Process -FilePath $AgentPath -ArgumentList $agentArgs -WindowStyle Hidden
    Start-Sleep -Seconds 4
    if (Get-Process swagcore-agent -ErrorAction SilentlyContinue) {
        Write-Ok "агент запущен (PID $((Get-Process swagcore-agent | Select-Object -First 1).Id))"
        $started = $true
    } else {
        Write-Err "не удалось запустить агента — смотрите лог: $LogPath"
    }
}

# ---- 8. Проверка, что агент ЖИВ и держит соединение с панелью ----
Write-Step "Проверяем, что агент жив и подключён"
# ВАЖНО (v0.7.0): раньше здесь проверялось здоровье СЕРВЕРА платформы
# (/api/health), а не подключение этой ноды. Поэтому установщик рапортовал
# об успехе даже когда служба лежала мёртвой. Теперь проверяем обе вещи.

# 8a. Служба должна продержаться дольше 30-секундного таймаута SCM.
# Именно на 30-й секунде SCM убивал агента (Event 7009). Ждём 35 с.
if ($asService) {
    Write-Host "    ждём 35 с — SCM рвёт службу на 30-й секунде, если нет рукопожатия…"
    Start-Sleep -Seconds 35
    $svc2 = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc2 -and $svc2.Status -eq "Running") {
        Write-Ok "служба пережила таймаут SCM и осталась Running"
    } else {
        Write-Err "служба НЕ осталась Running (статус: $(if($svc2){$svc2.Status}else{'нет сервиса'}))"
        Log "service did not stay Running after 35s (SCM handshake timeout?)"
        $asService = $false
    }
}

# 8b. Процесс агента жив.
$agentProc = Get-Process swagcore-agent -ErrorAction SilentlyContinue | Select-Object -First 1
if ($agentProc) {
    Write-Ok "процесс агента жив (PID $($agentProc.Id))"
} else {
    Write-Err "процесс агента не найден"
}

# 8c. Агент сам пишет в <DataDir>\agent.log — смотрим успешную сессию.
$agentLog = Join-Path $DataDir "agent.log"
$ok = $false
$maxTries = 15
for ($i = 1; $i -le $maxTries; $i++) {
    if (Test-Path $agentLog) {
        $tail = (Get-Content $agentLog -Tail 40 -ErrorAction SilentlyContinue) -join "`n"
        if ($tail -match "session started|connected|hello sent") {
            Write-Ok "агент подключился к панели (см. $agentLog)"
            $ok = $true
            break
        }
    }
    if (-not $agentProc) {
        Write-Err "процесс агента умер — установка не удалась"
        break
    }
    Write-Host "    попытка $i/$maxTries — ждём подключения…"
    Start-Sleep -Seconds 3
}

# 8d. Доступность самой платформы — отдельной информационной строкой.
try {
    $health = Invoke-RestMethod -Uri "https://$CoreHost/api/health" -TimeoutSec 15
    if ($health.ok) {
        Write-Ok "сервер платформы отвечает (версия $($health.version), нод онлайн: $($health.online))"
    }
} catch {
    Write-Warn "сервер платформы не ответил на /api/health: $($_.Exception.Message)"
}

Write-Host ""
Write-Host "  ────────────────────────────────────────────────" -ForegroundColor DarkGray
if ($ok) {
    Write-Host "  ГОТОВО. Нода подключена." -ForegroundColor Green
} else {
    Write-Host "  Установлено, но подключение пока не подтверждено." -ForegroundColor Yellow
}
Write-Host ""
Write-Host "  Панель:        https://$CoreHost/  ->  раздел «Ноды»" -ForegroundColor White
Write-Host "  Имя ноды:      $Name" -ForegroundColor White
Write-Host "  Режим:         $(if ($asService) { 'служба Windows (автозапуск + перезапуск)' } else { 'автозагрузка Windows' })" -ForegroundColor White
Write-Host "  Сборка агента: $localBuild" -ForegroundColor White
Write-Host "  Данные:        $DataDir" -ForegroundColor White
Write-Host "  Лог установки: $LogPath" -ForegroundColor White
Write-Host "  Лог агента:    $(Join-Path $DataDir 'agent.log')" -ForegroundColor White
Write-Host ""
Write-Host "  Что дальше:" -ForegroundColor White
Write-Host "   1) Откройте панель и убедитесь, что нода «$Name» в списке «на связи»." -ForegroundColor Gray
Write-Host "   2) Задеплойте проект: раздел «Проекты» -> «+ Задеплоить»." -ForegroundColor Gray
Write-Host "   3) Агент сам обновляется, когда выйдет новая сборка (раз в 30 мин)." -ForegroundColor Gray
Write-Host ""
Write-Host "  Отключить совсем: .\install.ps1 -Uninstall" -ForegroundColor DarkGray
Write-Host "  ────────────────────────────────────────────────" -ForegroundColor DarkGray

Log "=== install done ok=$ok mode=$(if($asService){'service'}else{'autostart'}) build=$localBuild ==="
exit 0
