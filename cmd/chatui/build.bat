@echo off
echo Building ClipGen-m ChatUI...

go build -o ClipGen-m-chatui.exe .

if %ERRORLEVEL% EQU 0 (
    echo Build completed successfully!
    echo Executable: ClipGen-m-chatui.exe
) else (
    echo Build failed!
    pause
    exit /b 1
)

pause