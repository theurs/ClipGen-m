@echo off
setlocal

set "APP_NAME=ClipGen-m-chatui.exe"
set "ICON_FILE=chatui.ico"
set "MANIFEST_FILE=main.manifest"
set "EMBED_DIR=internal\ui"

echo ==========================================
echo      Building ClipGen-m (Local Icon)
echo ==========================================

:: 1. ПРОВЕРКА: Файл иконки должен лежать рядом с батником
if not exist %ICON_FILE% (
    echo [ERROR] File '%ICON_FILE%' not found in project root!
    echo Please put your icon file here and name it 'chatui.ico'.
    pause
    exit /b 1
)

:: 2. Очистка старых системных файлов (но не самой иконки!)
if exist %APP_NAME% del %APP_NAME%
if exist rsrc.syso del rsrc.syso

:: 3. Сборка ресурсов для Windows (чтобы иконка была у файла в Проводнике)
echo [STEP 1/4] Packing resources with rsrc...
rsrc -manifest %MANIFEST_FILE% -ico %ICON_FILE% -o rsrc.syso

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] rsrc failed. Your icon format might be invalid.
    pause
    exit /b 1
)

:: 4. Копирование иконки для Go (чтобы иконка была в Окне и Панели задач)
:: Мы копируем файл из корня в папку пакета ui, чтобы go:embed его увидел.
echo [STEP 2/4] Copying icon for embedding...
copy /Y %ICON_FILE% %EMBED_DIR%\%ICON_FILE% >nul

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Failed to copy icon to internal\ui folder.
    echo Check if 'internal\ui' directory exists.
    pause
    exit /b 1
)

:: 5. Обновление модулей
echo [STEP 3/4] Tidy...
go mod tidy

:: 6. Компиляция
echo [STEP 4/4] Building EXE...
go build -ldflags "-H=windowsgui -s -w" -o %APP_NAME%

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Build failed!
    pause
    exit /b 1
)

echo.
echo [SUCCESS] Done!
echo Created: %APP_NAME%
echo.
pause