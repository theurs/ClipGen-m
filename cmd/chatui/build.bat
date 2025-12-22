@echo off
setlocal

set "APP_NAME=ClipGen-m-chatui.exe"
set "ICON_FILE=chatui.ico"
set "MANIFEST_FILE=main.manifest"
set "EMBED_DIR=internal\ui"

echo ==========================================
echo      Building ClipGen-m (Auto-Cleanup)
echo ==========================================

:: 1. ПРОВЕРКА: Файл иконки должен лежать рядом с батником
if not exist %ICON_FILE% (
    echo [ERROR] File '%ICON_FILE%' not found in project root!
    echo Please put your icon file here and name it 'chatui.ico'.
    pause
    exit /b 1
)

:: 2. Очистка перед сборкой (удаляем старый EXE и возможные остатки)
if exist %APP_NAME% del %APP_NAME%
if exist rsrc.syso del rsrc.syso
if exist %EMBED_DIR%\%ICON_FILE% del %EMBED_DIR%\%ICON_FILE%

:: 3. Сборка ресурсов для Windows (иконка файла)
echo [STEP 1/5] Packing resources with rsrc...
rsrc -manifest %MANIFEST_FILE% -ico %ICON_FILE% -o rsrc.syso

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] rsrc failed.
    pause
    exit /b 1
)

:: 4. Копирование иконки для Go (иконка окна)
echo [STEP 2/5] Copying icon for embedding...
copy /Y %ICON_FILE% %EMBED_DIR%\%ICON_FILE% >nul

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Failed to copy icon to internal\ui folder.
    pause
    exit /b 1
)

:: 5. Обновление модулей
echo [STEP 3/5] Tidy...
go mod tidy

:: 6. Компиляция
echo [STEP 4/5] Building EXE...
go build -ldflags "-H=windowsgui -s -w" -o %APP_NAME%

if %ERRORLEVEL% NEQ 0 (
    echo [ERROR] Build failed!
    :: Если сборка упала, мусор можно не удалять, чтобы видеть ошибки, 
    :: или удалить вручную. Здесь мы выходим.
    pause
    exit /b 1
)

:: 7. Удаление мусора (НОВЫЙ БЛОК)
echo [STEP 5/5] Cleaning up temporary files...
if exist rsrc.syso (
    del rsrc.syso
    echo    - Deleted rsrc.syso
)
if exist %EMBED_DIR%\%ICON_FILE% (
    del %EMBED_DIR%\%ICON_FILE%
    echo    - Deleted internal copy of icon
)

echo.
echo [SUCCESS] Done!
echo Created: %APP_NAME%
echo.
pause