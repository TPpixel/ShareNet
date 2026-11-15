@echo off
chcp 65001 >nul
setlocal

set "SRC=%~dp0"
if "%SRC:~-1%"=="\" set "SRC=%SRC:~0,-1%"
set "DST=%LOCALAPPDATA%\ShareNet"

echo Подготовка локального запуска ShareNet...
robocopy "%SRC%" "%DST%" /E /XO /NFL /NDL /NJH /NJS /NC /NS /NP >nul
start "" "%DST%\ShareNet.exe"
exit /b 0
