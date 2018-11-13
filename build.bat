@echo off
echo. 

pushd "%~dp0"

taskkill /f /im ycssite.exe 2>nul

go build

ycssite.exe

popd
