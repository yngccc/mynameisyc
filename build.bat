@echo off
echo. 

pushd "%~dp0"

taskkill /f /im mynameisyc.exe 2>nul

go build

mynameisyc.exe

popd
