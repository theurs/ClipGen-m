@echo off
setlocal enabledelayedexpansion

REM Read version from VERSION file
for /f "tokens=*" %%i in (VERSION) do set VERSION=%%i

echo Building release version !VERSION!

REM Create build folders
if not exist "dist" mkdir "dist"
if not exist "dist\windows-amd64" mkdir "dist\windows-amd64"

REM Build binaries
echo Building clipgen-m...
cd cmd\clipgen-m
go build -ldflags "-H=windowsgui" -o ..\..\dist\windows-amd64\clipgen-m.exe
if !errorlevel! neq 0 (
    echo Error building clipgen-m
    exit /b !errorlevel!
)
cd ..\..

echo Building chatui...
cd cmd\chatui

REM Prepare resources for chatui
if exist rsrc.syso del rsrc.syso
if exist internal\ui\chatui.ico del internal\ui\chatui.ico

REM Build Windows resources (icon and manifest)
rsrc -manifest main.manifest -ico chatui.ico -o rsrc.syso
if !errorlevel! neq 0 (
    echo Warning: rsrc failed to create resources, continuing build...
)

REM Copy icon for Go embed
copy /Y chatui.ico internal\ui\chatui.ico >nul

REM Build the executable
go build -ldflags "-H=windowsgui -s -w" -o ..\..\dist\windows-amd64\ClipGen-m-chatui.exe
if !errorlevel! neq 0 (
    echo Error building chatui
    exit /b !errorlevel!
)

REM Cleanup temporary files
if exist rsrc.syso del rsrc.syso
if exist internal\ui\chatui.ico del internal\ui\chatui.ico

cd ..\..

echo Building geminillm...
cd cmd\geminillm
go build -o ..\..\dist\windows-amd64\geminillm.exe
if !errorlevel! neq 0 (
    echo Error building geminillm
    exit /b !errorlevel!
)
cd ..\..

echo Building ghllm...
cd cmd\ghllm
go build -o ..\..\dist\windows-amd64\ghllm.exe
if !errorlevel! neq 0 (
    echo Error building ghllm
    exit /b !errorlevel!
)
cd ..\..

echo Building groqllm...
cd cmd\groqllm
go build -o ..\..\dist\windows-amd64\groqllm.exe
if !errorlevel! neq 0 (
    echo Error building groqllm
    exit /b !errorlevel!
)
cd ..\..

REM Copy version file to build folder
copy VERSION dist\windows-amd64\

REM Copy icons from clipgen-m folder to build folder
copy cmd\clipgen-m\icon.ico dist\windows-amd64\
copy cmd\clipgen-m\icon_wait.ico dist\windows-amd64\
copy cmd\clipgen-m\icon_stop.ico dist\windows-amd64\

REM Archive
echo Creating archive...
cd dist\windows-amd64
7z a -tzip "..\clipgen-m-v!VERSION!-windows-amd64.zip" *.*
if !errorlevel! neq 0 (
    echo Error archiving
    exit /b !errorlevel!
)
cd ..\..

REM Create GitHub release
echo Creating GitHub release...

REM Check if a release with the same tag already exists and delete it
gh release view "v!VERSION!" >nul 2>&1
if !errorlevel! equ 0 (
    echo Found existing release, deleting it...
    gh release delete "v!VERSION!" --yes 2>nul
)

REM Create the new release
gh release create "v!VERSION!" "dist\clipgen-m-v!VERSION!-windows-amd64.zip" --title "v!VERSION!" --notes "Release version !VERSION!"

if !errorlevel! equ 0 (
    echo Release v!VERSION! successfully created!
) else (
    echo Error creating release
    exit /b !errorlevel!
)

echo Build and publish completed!

REM Cleanup dist folder
echo Cleaning up dist folder...
del /q dist\windows-amd64\*
rmdir /s /q dist\windows-amd64