@echo off
rem Runs the test suite on Windows (cmd or PowerShell). Linux/WSL: test.sh.
rem
rem   test.cmd              all packages
rem   test.cmd -v           verbose
rem   test.cmd -race        with the race detector (needs a C toolchain, e.g. mingw-w64)
rem   test.cmd -cross       also type-check every package and test for linux/amd64
rem   test.cmd ./internal/screen/   only these packages
rem
rem The tmux-backed live test never runs on Windows (no tmux); everything
rem else is the same suite as on Linux. -count=1 always, so cached results
rem cannot hide flakes.
setlocal
cd /d "%~dp0"

set RACE=
set VERBOSE=
set CROSS=0
set PKGS=
:args
if "%~1"=="" goto run
if "%~1"=="-race" (set RACE=-race) else if "%~1"=="-v" (set VERBOSE=-v) else if "%~1"=="-cross" (set CROSS=1) else if "%~1"=="-h" (goto help) else (set PKGS=%PKGS% %~1)
shift
goto args

:help
findstr /b /c:"rem " "%~f0" | more
exit /b 0

:run
if "%PKGS%"=="" set PKGS=./...

for /f "delims=" %%f in ('gofmt -l cmd internal') do (
  echo gofmt: not formatted: %%f
  set BAD=1
)
if defined BAD exit /b 1

go vet %PKGS% || exit /b 1
go test -count=1 %RACE% %VERBOSE% %PKGS% || exit /b 1

if "%CROSS%"=="1" (
  echo --- linux/amd64 type-check ^(tests included^)
  set GOOS=linux
  set GOARCH=amd64
  set CGO_ENABLED=0
  go vet %PKGS% || exit /b 1
)
echo OK
endlocal
