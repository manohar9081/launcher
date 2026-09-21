@echo off
setlocal
set "PORT=8000"
if not "%~1"=="" set "PORT=%~1"
cd /d "%~dp0"
go run ..\StaticServer --root . --port %PORT% --host 0.0.0.0
