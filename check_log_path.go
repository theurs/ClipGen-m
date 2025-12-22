package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "clipgen-m", "clipgen.log")
	fmt.Println("Expected log path:", logPath)
	
	// Check if directory exists
	appDir := filepath.Join(configDir, "clipgen-m")
	fmt.Println("App directory:", appDir)
	
	_, err := os.Stat(appDir)
	if os.IsNotExist(err) {
		fmt.Println("App directory does not exist yet")
	} else {
		fmt.Println("App directory exists")
		
		// Check if log file exists
		_, err := os.Stat(logPath)
		if os.IsNotExist(err) {
			fmt.Println("Log file does not exist yet")
		} else {
			fmt.Println("Log file exists")
			
			// Read last few lines of log file
			content, err := os.ReadFile(logPath)
			if err == nil {
				fmt.Println("Log file content:")
				fmt.Println(string(content))
			}
		}
	}
}