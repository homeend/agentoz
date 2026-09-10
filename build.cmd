@echo off
rem Builds erbrus on Windows (cmd or PowerShell): bin\erbrus.exe for
rem windows/amd64 and bin\erbrus for linux/amd64. Linux/WSL: build.sh.
rem Pure-Go deps (modernc sqlite), so CGO stays off and both targets
rem cross-compile from anywhere.
setlocal
cd /d "%~dp0"

set CGO_ENABLED=0

set GOOS=windows
set GOARCH=amd64
go build -trimpath -o bin\erbrus.exe .\cmd\erbrus || exit /b 1

set GOOS=linux
set GOARCH=amd64
go build -trimpath -o bin\erbrus .\cmd\erbrus || exit /b 1

dir bin\erbrus.exe bin\erbrus
endlocal
