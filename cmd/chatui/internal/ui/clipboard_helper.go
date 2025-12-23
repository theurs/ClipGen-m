// file: internal/ui/clipboard_helper.go
package ui

import (
	"bytes"
	"image/color"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Windows API constants
const (
	CF_BITMAP    = 2
	CF_DIB       = 8
	CF_DIBV5     = 17
	CF_HDROP     = 15
)

// Windows API procedures
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
	globalSize                 = kernel32.NewProc("GlobalSize")
	dragQueryFile              = shell32.NewProc("DragQueryFileW")
)

// BITMAPFILEHEADER structure
type BITMAPFILEHEADER struct {
	BfType      uint16
	BfSize      uint32
	BfReserved1 uint16
	BfReserved2 uint16
	BfOffBits   uint32
}

// tryOpenClipboard attempts to open the clipboard with retries
func tryOpenClipboard() (bool, error) {
	for i := 0; i < 20; i++ {
		r, _, _ := openClipboard.Call(0)
		if r != 0 {
			return true, nil
		}
		// Sleep for 10ms between attempts
		syscall.Syscall(kernel32.NewProc("Sleep").Addr(), 1, 10, 0, 0)
	}
	return false, fmt.Errorf("не удалось открыть буфер обмена (занят другим приложением)")
}

// HasClipboardFiles проверяет, есть ли в буфере файлы (копирование из проводника)
func HasClipboardFiles() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Check if CF_HDROP format is available
	r, _, _ := isClipboardFormatAvailable.Call(CF_HDROP)
	return r != 0
}

// GetClipboardFiles возвращает список путей
func GetClipboardFiles() ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Check if CF_HDROP format is available
	r, _, _ := isClipboardFormatAvailable.Call(CF_HDROP)
	if r == 0 {
		return nil, fmt.Errorf("CF_HDROP не доступен в буфере обмена")
	}

	// Try to open clipboard
	if success, err := tryOpenClipboard(); !success {
		return nil, err
	}
	defer closeClipboard.Call()

	// Get clipboard data handle
	hDrop, _, _ := getClipboardData.Call(CF_HDROP)
	if hDrop == 0 {
		return nil, fmt.Errorf("GetClipboardData failed for CF_HDROP")
	}

	// Get number of files
	cnt, _, _ := dragQueryFile.Call(hDrop, 0xFFFFFFFF, 0, 0)
	if cnt == 0 {
		return nil, fmt.Errorf("no files in CF_HDROP")
	}

	var files []string
	for i := uintptr(0); i < cnt; i++ {
		// Get required buffer size
		lenRet, _, _ := dragQueryFile.Call(hDrop, i, 0, 0)
		if lenRet == 0 {
			continue
		}

		// Create buffer and get the file path
		buf := make([]uint16, lenRet+1)
		dragQueryFile.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), lenRet+1)
		filePath := syscall.UTF16ToString(buf)
		files = append(files, filePath)
	}

	return files, nil
}

// HasClipboardImage проверяет, есть ли картинка в буфере
func HasClipboardImage() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Check if CF_DIB format is available
	r, _, _ := isClipboardFormatAvailable.Call(CF_DIB)
	return r != 0
}

// GetClipboardImageFromDIB reads image from clipboard in DIB format
func GetClipboardImageFromDIB() ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Check if CF_DIB format is available
	r, _, _ := isClipboardFormatAvailable.Call(CF_DIB)
	if r == 0 {
		return nil, fmt.Errorf("CF_DIB не доступен в буфере обмена")
	}

	// Try to open clipboard
	if success, err := tryOpenClipboard(); !success {
		return nil, err
	}
	defer closeClipboard.Call()

	// Get clipboard data handle
	hMem, _, _ := getClipboardData.Call(CF_DIB)
	if hMem == 0 {
		return nil, fmt.Errorf("GetClipboardData failed")
	}

	// Lock the memory to access the data
	pData, _, _ := globalLock.Call(hMem)
	if pData == 0 {
		return nil, fmt.Errorf("GlobalLock failed")
	}
	defer globalUnlock.Call(hMem)

	// Get the size of the data
	memSize, _, _ := globalSize.Call(hMem)
	if memSize == 0 {
		return nil, fmt.Errorf("GlobalSize returned 0")
	}

	// Read the DIB data
	dibData := make([]byte, memSize)
	if memSize > 0 {
		srcSlice := (*[1 << 30]byte)(unsafe.Pointer(pData))[:memSize:memSize]
		copy(dibData, srcSlice)
	}

	// For Windows clipboard DIB data, we might need to handle it differently
	// The DIB data in clipboard is often just the DIB structure without the file header
	// We'll return the raw DIB data and let the decoder handle it
	return dibData, nil
}

// SaveClipboardImageToTemp сохраняет картинку из буфера в файл
// createImageFrom32BitDIB creates an image from 32-bit DIB data (BGRA format)
func createImageFrom32BitDIB(dibData []byte) (image.Image, error) {
	if len(dibData) < 16 {
		return nil, fmt.Errorf("недостаточно данных")
	}

	biWidth := int32(binary.LittleEndian.Uint32(dibData[4:8]))
	biHeight := int32(binary.LittleEndian.Uint32(dibData[8:12]))

	if biWidth <= 0 || biHeight <= 0 || biWidth > 10000 || biHeight > 10000 {
		return nil, fmt.Errorf("некорректные размеры изображения: %dx%d", biWidth, biHeight)
	}

	// DIB data starts after the header (usually 40 bytes for BITMAPINFOHEADER)
	headerSize := binary.LittleEndian.Uint32(dibData[0:4])
	if headerSize < 40 {
		return nil, fmt.Errorf("некорректный размер заголовка: %d", headerSize)
	}

	imageDataStart := int(headerSize)
	if len(dibData) <= imageDataStart {
		return nil, fmt.Errorf("недостаточно данных для изображения")
	}

	imageData := dibData[imageDataStart:]

	// Calculate expected image data size for 32-bit image
	expectedSize := int(biWidth) * int(biHeight) * 4
	if len(imageData) < expectedSize {
		return nil, fmt.Errorf("недостаточно данных для изображения размером %dx%d", biWidth, biHeight)
	}

	// Create Go image from BGRA data
	img := image.NewRGBA(image.Rect(0, 0, int(biWidth), int(biHeight)))

	// DIB data is stored bottom-up, so we need to flip vertically
	for y := 0; y < int(biHeight); y++ {
		for x := 0; x < int(biWidth); x++ {
			srcIdx := (int(biHeight)-1-y)*int(biWidth)*4 + x*4  // Flip vertically
			if srcIdx+3 < len(imageData) {
				// BGRA format: Blue, Green, Red, Alpha
				b := imageData[srcIdx]
				g := imageData[srcIdx+1]
				r := imageData[srcIdx+2]
				a := imageData[srcIdx+3]

				img.SetRGBA(x, y, color.RGBA{r, g, b, a})
			}
		}
	}

	return img, nil
}

// createImageFrom24BitDIB creates an image from 24-bit DIB data (BGR format)
func createImageFrom24BitDIB(dibData []byte) (image.Image, error) {
	if len(dibData) < 16 {
		return nil, fmt.Errorf("недостаточно данных")
	}

	biWidth := int32(binary.LittleEndian.Uint32(dibData[4:8]))
	biHeight := int32(binary.LittleEndian.Uint32(dibData[8:12]))

	if biWidth <= 0 || biHeight <= 0 || biWidth > 10000 || biHeight > 10000 {
		return nil, fmt.Errorf("некорректные размеры изображения: %dx%d", biWidth, biHeight)
	}

	// DIB data starts after the header (usually 40 bytes for BITMAPINFOHEADER)
	headerSize := binary.LittleEndian.Uint32(dibData[0:4])
	if headerSize < 40 {
		return nil, fmt.Errorf("некорректный размер заголовка: %d", headerSize)
	}

	imageDataStart := int(headerSize)
	if len(dibData) <= imageDataStart {
		return nil, fmt.Errorf("недостаточно данных для изображения")
	}

	imageData := dibData[imageDataStart:]

	// Calculate expected image data size for 24-bit image (with 4-byte row alignment)
	rowSize := ((int(biWidth) * 3) + 3) &^ 3  // Round up to multiple of 4
	expectedSize := rowSize * int(biHeight)
	if len(imageData) < expectedSize {
		return nil, fmt.Errorf("недостаточно данных для изображения размером %dx%d", biWidth, biHeight)
	}

	// Create Go image from BGR data
	img := image.NewRGBA(image.Rect(0, 0, int(biWidth), int(biHeight)))

	// DIB data is stored bottom-up, so we need to flip vertically
	for y := 0; y < int(biHeight); y++ {
		for x := 0; x < int(biWidth); x++ {
			srcIdx := (int(biHeight)-1-y)*rowSize + x*3  // Flip vertically
			if srcIdx+2 < len(imageData) {
				// BGR format: Blue, Green, Red
				b := imageData[srcIdx]
				g := imageData[srcIdx+1]
				r := imageData[srcIdx+2]

				img.SetRGBA(x, y, color.RGBA{r, g, b, 255}) // Full opacity
			}
		}
	}

	return img, nil
}

// SaveClipboardImageToTemp сохраняет картинку из буфера в файл
func SaveClipboardImageToTemp() (string, error) {
	// Get image data from clipboard
	imgData, err := GetClipboardImageFromDIB()
	if err != nil {
		return "", err
	}

	// Try to decode the raw DIB data directly
	img, _, decodeErr := image.Decode(bytes.NewReader(imgData))
	if decodeErr != nil {
		// If direct decoding fails, try manual conversion using DIB header info
		if len(imgData) >= 16 {
			biWidth := int32(binary.LittleEndian.Uint32(imgData[4:8]))
			biHeight := int32(binary.LittleEndian.Uint32(imgData[8:12]))
			biBitCount := binary.LittleEndian.Uint16(imgData[14:16])

			if biWidth > 0 && biHeight > 0 && biWidth < 10000 && biHeight < 10000 {
				// Try to create image from raw DIB data for 32-bit images
				if biBitCount == 32 {
					img, err = createImageFrom32BitDIB(imgData)
					if err == nil && img != nil {
						// Successfully created image from 32-bit DIB data
					} else {
						return "", fmt.Errorf("не удалось создать изображение из 32-битных DIB данных: %v", err)
					}
				} else if biBitCount == 24 {
					img, err = createImageFrom24BitDIB(imgData)
					if err == nil && img != nil {
						// Successfully created image from 24-bit DIB data
					} else {
						return "", fmt.Errorf("не удалось создать изображение из 24-битных DIB данных: %v", err)
					}
				} else {
					// If bit count is not 24 or 32, try creating BMP file with proper header
					infoHeaderSize := binary.LittleEndian.Uint32(imgData[0:4])
					if infoHeaderSize >= 40 {
						// Create BMP file with proper header
						bmpHeader := BITMAPFILEHEADER{
							BfType:      0x4D42, // 'BM'
							BfSize:      uint32(14 + len(imgData)),
							BfReserved1: 0,
							BfReserved2: 0,
							BfOffBits:   uint32(14 + infoHeaderSize),
						}

						buf := new(bytes.Buffer)
						binary.Write(buf, binary.LittleEndian, &bmpHeader)
						buf.Write(imgData)

						// Now try to decode the complete BMP file
						img, _, decodeErr = image.Decode(buf)
						if decodeErr != nil {
							return "", fmt.Errorf("изображение не является валидным: %v", decodeErr)
						}
					} else {
						return "", fmt.Errorf("изображение не является валидным: %v", decodeErr)
					}
				}
			} else {
				return "", fmt.Errorf("изображение не является валидным: %v", decodeErr)
			}
		} else {
			return "", fmt.Errorf("изображение не является валидным: %v", decodeErr)
		}
	}

	// Create temp file
	tempName := fmt.Sprintf("clipgen_paste_%d.png", time.Now().UnixNano())
	tempPath := filepath.Join(os.TempDir(), tempName)

	// Save as PNG
	file, err := os.Create(tempPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		return "", err
	}

	return tempPath, nil
}
