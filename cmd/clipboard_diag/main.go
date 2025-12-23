package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	_ "image/jpeg"
	_ "golang.org/x/image/bmp"
)

// Windows API constants
const (
	CF_BITMAP    = 2
	CF_DIB       = 8
	CF_DIBV5     = 17
	CF_HDROP     = 15
	CF_TIFF      = 18
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

// getClipboardImageFromDIB reads image from clipboard in DIB format
func getClipboardImageFromDIB() ([]byte, error) {
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

	// Get the size of the data first
	memSize, _, _ := globalSize.Call(hMem)
	if memSize == 0 {
		return nil, fmt.Errorf("GlobalSize returned 0")
	}

	// Lock the memory to access the data
	pData, _, _ := globalLock.Call(hMem)
	if pData == 0 {
		return nil, fmt.Errorf("GlobalLock failed")
	}
	defer globalUnlock.Call(hMem)

	// Read the DIB data
	dibData := make([]byte, memSize)
	if memSize > 0 {
		srcSlice := (*[1 << 30]byte)(unsafe.Pointer(pData))[:memSize:memSize]
		copy(dibData, srcSlice)
	}

	// For Windows clipboard DIB data, we return the raw data
	// The proper BMP file creation is handled separately
	return dibData, nil
}

// tryClipboardFormat пробует получить изображение из определенного формата буфера
func tryClipboardFormat(format uintptr) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Check if the format is available
	r, _, _ := isClipboardFormatAvailable.Call(format)
	if r == 0 {
		return nil, fmt.Errorf("формат %d не доступен в буфере обмена", format)
	}

	// Try to open clipboard
	if success, err := tryOpenClipboard(); !success {
		return nil, err
	}
	defer closeClipboard.Call()

	// Get clipboard data handle
	hMem, _, _ := getClipboardData.Call(format)
	if hMem == 0 {
		return nil, fmt.Errorf("GetClipboardData failed for format %d", format)
	}

	// Get the size of the data first
	memSize, _, _ := globalSize.Call(hMem)
	if memSize == 0 {
		return nil, fmt.Errorf("GlobalSize returned 0 for format %d", format)
	}

	// Lock the memory to access the data
	pData, _, _ := globalLock.Call(hMem)
	if pData == 0 {
		return nil, fmt.Errorf("GlobalLock failed for format %d", format)
	}
	defer globalUnlock.Call(hMem)

	// Read the data
	imgData := make([]byte, memSize)
	if memSize > 0 {
		srcSlice := (*[1 << 30]byte)(unsafe.Pointer(pData))[:memSize:memSize]
		copy(imgData, srcSlice)
	}

	return imgData, nil
}

// decodeClipboardImage tries to decode clipboard image with various approaches
func decodeClipboardImage() (image.Image, string, []byte, error) {
	// Try different clipboard formats in order of preference
	// Skip CF_BITMAP as it can cause issues with GlobalSize
	formats := []struct {
		id   uintptr
		name string
	}{
		{CF_DIB, "CF_DIB"},
		{CF_DIBV5, "CF_DIBV5"},
	}

	for _, format := range formats {
		fmt.Printf("     Попытка чтения из формата %s...\n", format.name)
		imgData, err := tryClipboardFormat(format.id)
		if err != nil {
			fmt.Printf("     Формат %s недоступен: %v\n", format.name, err)
			continue
		}

		fmt.Printf("     ✓ Найдены данные в формате %s, размер: %d байт\n", format.name, len(imgData))

		// Try to decode the raw data directly
		img, formatStr, decodeErr := image.Decode(bytes.NewReader(imgData))
		if decodeErr == nil {
			fmt.Printf("     ✓ Успешно декодировано как %s\n", formatStr)
			return img, formatStr, imgData, nil
		}

		// If direct decoding fails, try with BMP header for DIB formats
		if format.id == CF_DIB || format.id == CF_DIBV5 {
			if len(imgData) >= 4 {
				infoHeaderSize := binary.LittleEndian.Uint32(imgData[0:4])
				if infoHeaderSize >= 40 { // Minimum size for BITMAPINFOHEADER
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

					// Try to decode the complete BMP file
					img, formatStr, decodeErr := image.Decode(buf)
					if decodeErr == nil {
						fmt.Printf("     ✓ Успешно декодировано как BMP %s\n", formatStr)
						return img, formatStr, buf.Bytes(), nil
					}
				}
			}
		}
	}

	// If all formats failed, try to get raw DIB data as fallback
	imgData, err := getClipboardImageFromDIB()
	if err == nil {
		fmt.Printf("     ✓ Получены DIB данные, размер: %d байт\n", len(imgData))

		// Try to create a proper BMP file from DIB data
		if len(imgData) >= 16 { // Need at least size for basic header fields
			// Read BITMAPINFOHEADER values
			biSize := binary.LittleEndian.Uint32(imgData[0:4])
			biWidth := int32(binary.LittleEndian.Uint32(imgData[4:8]))
			biHeight := int32(binary.LittleEndian.Uint32(imgData[8:12]))
			biPlanes := binary.LittleEndian.Uint16(imgData[12:14])
			biBitCount := binary.LittleEndian.Uint16(imgData[14:16])

			fmt.Printf("     DIB Header Info: Size=%d, Width=%d, Height=%d, Planes=%d, BitCount=%d\n",
				biSize, biWidth, biHeight, biPlanes, biBitCount)

			if biSize >= 40 { // Minimum size for BITMAPINFOHEADER
				// Calculate the offset to image bits considering color table
				var colorTableSize int
				if biBitCount <= 8 {
					// For images with <= 8 bits per pixel, there might be a color table
					// The number of colors is usually 1 << biBitCount, but we'll use a safe approach
					// For 1, 4, 8 bpp images, color table size = number of colors * 4 (RGBA per color)
					if biSize == 40 { // BITMAPINFOHEADER
						colorTableSize = (1 << biBitCount) * 4
						// But we should check biClrUsed field (offset 32-35)
						// For safety, limit the color table size
						if colorTableSize > 1024 { // Reasonable limit
							colorTableSize = 0
						}
					}
				}

				// Calculate the offset: BMP header + info header + color table
				bfOffBits := uint32(14 + biSize + uint32(colorTableSize))

				// Create BMP file with proper header
				bmpHeader := BITMAPFILEHEADER{
					BfType:      0x4D42, // 'BM'
					BfSize:      uint32(14 + len(imgData)),
					BfReserved1: 0,
					BfReserved2: 0,
					BfOffBits:   bfOffBits,
				}

				buf := new(bytes.Buffer)
				binary.Write(buf, binary.LittleEndian, &bmpHeader)
				buf.Write(imgData)

				// Try to decode the complete BMP file
				img, formatStr, decodeErr := image.Decode(buf)
				if decodeErr == nil {
					fmt.Printf("     ✓ Успешно декодировано как BMP %s\n", formatStr)
					return img, formatStr, buf.Bytes(), nil
				} else {
					fmt.Printf("     ⚠ BMP декодирование не удалось: %v\n", decodeErr)

					// Try alternative approach - maybe the biSize is wrong or there's alignment
					// Let's try with just basic header
					bmpHeader.BfOffBits = uint32(14 + biSize)
					buf.Reset()
					binary.Write(buf, binary.LittleEndian, &bmpHeader)
					buf.Write(imgData)

					img, formatStr, decodeErr := image.Decode(buf)
					if decodeErr == nil {
						fmt.Printf("     ✓ Успешно декодировано как BMP (альтернативный подход) %s\n", formatStr)
						return img, formatStr, buf.Bytes(), nil
					}
				}
			} else {
				fmt.Printf("     ⚠ Недостаточный размер заголовка DIB: %d\n", biSize)
			}
		}

		// Even if we can't decode the image, return the raw data so we can save it
		return nil, "", imgData, fmt.Errorf("не удалось декодировать изображение в известном формате")
	}

	return nil, "", nil, fmt.Errorf("не удалось получить изображение из буфера обмена")
}

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

// getClipboardFiles reads file paths from clipboard if CF_HDROP is available
func getClipboardFiles() ([]string, error) {
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

// testClipboardContent tests what's in the clipboard
func testClipboardContent() {
	fmt.Println("=== Clipboard Diagnostic Tool ===")
	
	// Test for image data (DIB format)
	fmt.Println("\n1. Checking for image data (DIB format)...")

	// First check if DIB format is available at all
	runtime.LockOSThread()
	r, _, _ := isClipboardFormatAvailable.Call(CF_DIB)
	if r != 0 {
		fmt.Println("   ✓ CF_DIB format is available in clipboard")

		// Try to get and decode the image
		img, format, fullImgData, err := decodeClipboardImage()
		if err == nil && img != nil {
			fmt.Printf("   ✓ Successfully decoded image! Size: %d bytes\n", len(fullImgData))

			bounds := img.Bounds()
			fmt.Printf("   ✓ Successfully decoded as %s: %dx%d pixels\n", format, bounds.Dx(), bounds.Dy())

			// Additional image info
			fmt.Printf("   ✓ Image format: %s\n", format)
			fmt.Printf("   ✓ Image dimensions: %d x %d pixels\n", bounds.Dx(), bounds.Dy())
			fmt.Printf("   ✓ Image bounds: Min(%d,%d) to Max(%d,%d)\n", bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)

			// Save to temp file for verification
			tempDir := os.TempDir()
			tempFile := filepath.Join(tempDir, "clipboard_image_test.png")
			if file, err := os.Create(tempFile); err == nil {
				png.Encode(file, img)
				file.Close()
				fmt.Printf("   ✓ Image saved to: %s\n", tempFile)
			}
		} else if len(fullImgData) > 0 && img != nil {
			// We have raw data but couldn't decode it - still worth noting
			fmt.Printf("   ✓ Raw image data found! Size: %d bytes\n", len(fullImgData))
			fmt.Printf("   ⚠ Could not decode image: %v\n", err)

			// Still save the raw data for analysis
			tempDir := os.TempDir()
			dibFile := filepath.Join(tempDir, "clipboard_image_raw.dib")
			if file, err := os.Create(dibFile); err == nil {
				file.Write(fullImgData)
				file.Close()
				fmt.Printf("   ✓ Raw DIB data saved to: %s\n", dibFile)
			}
		} else if img != nil {
			// We have a decoded image, save it in multiple formats
			fmt.Printf("   ✓ Successfully decoded image! Size: %d bytes\n", len(fullImgData))

			bounds := img.Bounds()
			fmt.Printf("   ✓ Successfully decoded as %s: %dx%d pixels\n", format, bounds.Dx(), bounds.Dy())

			// Additional image info
			fmt.Printf("   ✓ Image format: %s\n", format)
			fmt.Printf("   ✓ Image dimensions: %d x %d pixels\n", bounds.Dx(), bounds.Dy())
			fmt.Printf("   ✓ Image bounds: Min(%d,%d) to Max(%d,%d)\n", bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)

			// Save to temp file in PNG format
			tempDir := os.TempDir()
			pngFile := filepath.Join(tempDir, "clipboard_image_test.png")
			if file, err := os.Create(pngFile); err == nil {
				png.Encode(file, img)
				file.Close()
				fmt.Printf("   ✓ PNG image saved to: %s\n", pngFile)
			}

			// Save to temp file in JPEG format
			jpgFile := filepath.Join(tempDir, "clipboard_image_test.jpg")
			if file, err := os.Create(jpgFile); err == nil {
				// Use default JPEG quality (75)
				jpeg.Encode(file, img, &jpeg.Options{Quality: 90})
				file.Close()
				fmt.Printf("   ✓ JPEG image saved to: %s\n", jpgFile)
			}

			// Try to save original DIB data too
			dibFile := filepath.Join(tempDir, "clipboard_image_raw.dib")
			if file, err := os.Create(dibFile); err == nil {
				file.Write(fullImgData)
				file.Close()
				fmt.Printf("   ✓ Raw DIB data saved to: %s\n", dibFile)
			}
		} else if len(fullImgData) > 0 {
			// We have raw DIB data but couldn't decode it - try to create image from raw DIB
			fmt.Printf("   ✓ Raw image data found! Size: %d bytes\n", len(fullImgData))
			fmt.Printf("   ⚠ Could not decode image directly, trying manual conversion...\n")

			// Try to create image from DIB data manually
			if len(fullImgData) >= 16 {
				biWidth := int32(binary.LittleEndian.Uint32(fullImgData[4:8]))
				biHeight := int32(binary.LittleEndian.Uint32(fullImgData[8:12]))
				biBitCount := binary.LittleEndian.Uint16(fullImgData[14:16])

				fmt.Printf("   ✓ DIB Info: %dx%d, %d-bit\n", biWidth, biHeight, biBitCount)

				if biWidth > 0 && biHeight > 0 && biWidth < 10000 && biHeight < 10000 {
					// Try to create image from raw DIB data for 32-bit images
					if biBitCount == 32 {
						img, err := createImageFrom32BitDIB(fullImgData)
						if err == nil && img != nil {
							tempDir := os.TempDir()
							jpgFile := filepath.Join(tempDir, "clipboard_image_test.jpg")
							if file, err := os.Create(jpgFile); err == nil {
								jpeg.Encode(file, img, &jpeg.Options{Quality: 90})
								file.Close()
								fmt.Printf("   ✓ JPEG image saved to: %s\n", jpgFile)
							}

							pngFile := filepath.Join(tempDir, "clipboard_image_test.png")
							if file, err := os.Create(pngFile); err == nil {
								png.Encode(file, img)
								file.Close()
								fmt.Printf("   ✓ PNG image saved to: %s\n", pngFile)
							}
						} else {
							fmt.Printf("   ⚠ Manual conversion failed: %v\n", err)
						}
					} else if biBitCount == 24 {
						img, err := createImageFrom24BitDIB(fullImgData)
						if err == nil && img != nil {
							tempDir := os.TempDir()
							jpgFile := filepath.Join(tempDir, "clipboard_image_test.jpg")
							if file, err := os.Create(jpgFile); err == nil {
								jpeg.Encode(file, img, &jpeg.Options{Quality: 90})
								file.Close()
								fmt.Printf("   ✓ JPEG image saved to: %s\n", jpgFile)
							}
						} else {
							fmt.Printf("   ⚠ Manual conversion failed: %v\n", err)
						}
					}
				}
			}

			// Save original DIB data
			tempDir := os.TempDir()
			dibFile := filepath.Join(tempDir, "clipboard_image_raw.dib")
			if file, err := os.Create(dibFile); err == nil {
				file.Write(fullImgData)
				file.Close()
				fmt.Printf("   ✓ Raw DIB data saved to: %s\n", dibFile)
			}
		} else {
			fmt.Printf("   ✗ No image data could be retrieved: %v\n", err)
		}
	} else {
		fmt.Println("   ✗ CF_DIB format not available in clipboard")
	}
	runtime.UnlockOSThread()

	// Test for file data (HDROP format)
	fmt.Println("\n2. Checking for file paths (HDROP format)...")
	if files, err := getClipboardFiles(); err == nil {
		fmt.Printf("   ✓ Found %d file(s):\n", len(files))
		for i, file := range files {
			fmt.Printf("   %d. %s\n", i+1, file)
		}
	} else {
		fmt.Printf("   ✗ No file paths found: %v\n", err)
	}

	// Test for text data
	fmt.Println("\n3. Checking for text data...")
	if success, err := tryOpenClipboard(); success {
		defer closeClipboard.Call()
		
		// Check for text format
		r, _, _ := isClipboardFormatAvailable.Call(1) // CF_TEXT
		if r != 0 {
			hMem, _, _ := getClipboardData.Call(1)
			if hMem != 0 {
				pData, _, _ := globalLock.Call(hMem)
				if pData != 0 {
					defer globalUnlock.Call(hMem)
					// Convert to Go string (null-terminated)
					var text strings.Builder
					for i := 0; ; i++ {
						char := *(*byte)(unsafe.Pointer(pData + uintptr(i)))
						if char == 0 {
							break
						}
						text.WriteByte(char)
					}
					textStr := text.String()
					if len(textStr) > 100 {
						fmt.Printf("   ✓ Found text (truncated): %s...\n", textStr[:100])
					} else if len(textStr) > 0 {
						fmt.Printf("   ✓ Found text: %s\n", textStr)
					} else {
						fmt.Printf("   ✗ Found empty text\n")
					}
				}
			}
		} else {
			fmt.Printf("   ✗ No text found in clipboard\n")
		}
	} else {
		fmt.Printf("   ✗ Could not open clipboard to check for text: %v\n", err)
	}

	fmt.Println("\n=== Diagnostic Complete ===")
}

func main() {
	// Enable Windows console output for non-English characters
	kernel32.NewProc("SetConsoleOutputCP").Call(uintptr(65001)) // UTF-8

	// Run the diagnostic
	testClipboardContent()

	// Wait for user input before exiting
	fmt.Println("\nPress Enter to exit...")
	fmt.Scanln()
}