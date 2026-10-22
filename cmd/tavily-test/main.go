package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// TavilySearchResponse represents the response from Tavily API
type TavilySearchResponse struct {
	Success    bool     `json:"success"`  // Some responses may include this
	Error      string   `json:"error,omitempty"`
	Query      string   `json:"query"`
	Answer     string   `json:"answer"`
	Result     string   `json:"result"`
	Images     []string `json:"images,omitempty"`
	Results    []struct {
		URL       string  `json:"url"`
		Title     string  `json:"title"`
		Content   string  `json:"content"`
		Score     float64 `json:"score"`
		RawContent *string `json:"raw_content"`
	} `json:"results"`  // This is the field used in the actual response
	WebResults []struct {
		Title     string `json:"title"`
		URL       string `json:"url"`
		Content   string `json:"content"`
		Relevance string `json:"relevance"`
	} `json:"web_results"`
	RequestID string  `json:"request_id"`
	ResponseTime float64 `json:"response_time"`
}

// Config represents the Tavily configuration
type Config struct {
	ApiKeys []string `json:"api_keys"`
}

func getConfigPath() (string, error) {
	// Get user config directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	// Create path to clipgen-m directory
	clipgenDir := filepath.Join(configDir, "clipgen-m")

	// Ensure directory exists
	if _, err := os.Stat(clipgenDir); os.IsNotExist(err) {
		_ = os.MkdirAll(clipgenDir, 0755)
	}

	// Return path to tavily.conf in the same directory as mistral.conf
	return filepath.Join(clipgenDir, "tavily.conf"), nil
}

func loadTavilyConfig() (*Config, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tavily.conf file does not exist at %s", configPath)
	}

	// Read the config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func main() {
	// Try to load Tavily API key from config file
	fmt.Println("Loading Tavily config...")
	config, err := loadTavilyConfig()
	if err != nil {
		fmt.Printf("Error loading Tavily config: %v\n", err)
		fmt.Println("Please create tavily.conf file in the same directory as mistral.conf with your API key(s)")
		return
	}

	fmt.Printf("Successfully loaded config with %d API key(s)\n", len(config.ApiKeys))

	if len(config.ApiKeys) == 0 {
		fmt.Println("Error: No API keys found in tavily.conf")
		return
	}

	// Print the API keys (partially masked for security)
	for i, key := range config.ApiKeys {
		if len(key) > 8 {
			fmt.Printf("Key %d: %s... (length: %d)\n", i+1, key[:8], len(key))
		} else {
			fmt.Printf("Key %d: %s (length: %d)\n", i+1, key, len(key))
		}
	}

	// Get search query from command line arguments
	if len(os.Args) < 2 {
		fmt.Println("Usage: tavily-test \"search query\"")
		return
	}

	query := os.Args[1]
	fmt.Printf("Search query: %s\n", query)

	// Try each API key until one works or we run out of keys
	var searchResp TavilySearchResponse
	var lastError error

	for i, apiKey := range config.ApiKeys {
		fmt.Printf("Attempting request with key %d: %s...\n", i+1, apiKey[:min(8, len(apiKey))]+"...")
		if apiKey == "" {
			fmt.Println("Skipping empty key")
			continue // Skip empty keys
		}

		// Prepare the request payload
		payload := map[string]interface{}{
			"api_key":           apiKey,
			"query":             query,
			"search_depth":      "basic", // or "advanced"
			"include_images":    false,
			"include_answer":    true,
			"include_raw_content": false,
			"max_results":       5,
		}

		// Convert payload to JSON
		jsonData, err := json.Marshal(payload)
		if err != nil {
			fmt.Printf("Error marshaling JSON: %v\n", err)
			continue // Try next key
		}

		fmt.Printf("Request payload: %s\n", string(jsonData))

		// Make the request to Tavily API
		resp, err := http.Post(
			"https://api.tavily.com/search",
			"application/json",
			// Using io.NopCloser to wrap the JSON data in an io.ReadCloser
			&readCloser{data: jsonData},
		)
		if err != nil {
			lastError = err
			fmt.Printf("Error making request with key: %v\n", err)
			continue // Try next key
		}
		defer resp.Body.Close()

		// Read the response
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastError = err
			fmt.Printf("Error reading response: %v\n", err)
			continue // Try next key
		}

		fmt.Printf("Raw response: %s\n", string(body))

		// Parse the response
		var tempResp TavilySearchResponse
		err = json.Unmarshal(body, &tempResp)
		if err != nil {
			lastError = err
			fmt.Printf("Error unmarshaling response: %v\n", err)
			continue // Try next key
		}

		fmt.Printf("Parsed response - Success: %t, Error: %s\n", tempResp.Success, tempResp.Error)

		// Check if the request was successful
		// In Tavily API, if there's no error and we have results or an answer, consider it successful
		if tempResp.Error == "" && (len(tempResp.Results) > 0 || tempResp.Answer != "" || len(tempResp.WebResults) > 0) {
			fmt.Printf("Request successful with key %d\n", i+1)
			searchResp = tempResp
			// Success! Break out of the loop
			break
		} else {
			// Check if the error is related to the API key
			if tempResp.Error != "" {
				if containsKeyError(tempResp.Error) {
					fmt.Printf("Key error: %s, trying next key...\n", tempResp.Error)
					lastError = fmt.Errorf("key error: %s", tempResp.Error)
					continue // Try next key
				} else {
					// Some other error that might not be key-related
					fmt.Printf("API error (not key related): %s, trying next key...\n", tempResp.Error)
					lastError = fmt.Errorf("API error: %s", tempResp.Error)
					continue // Try next key anyway
				}
			} else {
				// No explicit error but no expected results - this may indicate the issue
				fmt.Printf("Request returned no results without specific error, trying next key...\n")
				lastError = fmt.Errorf("request returned no results without specific error")
				continue
			}
		}
	}

	// If we exhausted all keys, report the last error
	// We should also check if we have valid results rather than just the Success field
	if searchResp.Error == "" && (len(searchResp.Results) > 0 || searchResp.Answer != "" || len(searchResp.WebResults) > 0) {
		// We have a successful response with results
		// Print the results
		fmt.Printf("Query: %s\n", searchResp.Query)
		fmt.Printf("Answer: %s\n", searchResp.Answer)
		fmt.Printf("Result: %s\n", searchResp.Result)
		fmt.Printf("\nTop web results:\n")
		for i, result := range searchResp.Results {
			fmt.Printf("%d. %s\n", i+1, result.Title)
			fmt.Printf("   URL: %s\n", result.URL)
			fmt.Printf("   Content: %s\n\n", result.Content)
		}
		return
	} else {
		// No successful results were obtained
		if lastError != nil {
			fmt.Printf("All API keys failed. Last error: %v\n", lastError)
		} else {
			fmt.Printf("All API keys failed without specific error\n")
		}
		return
	}

	// Print the results
	fmt.Printf("Query: %s\n", searchResp.Query)
	fmt.Printf("Answer: %s\n", searchResp.Answer)
	fmt.Printf("Result: %s\n", searchResp.Result)
	fmt.Printf("\nTop web results:\n")
	for i, result := range searchResp.WebResults {
		fmt.Printf("%d. %s\n", i+1, result.Title)
		fmt.Printf("   URL: %s\n", result.URL)
		fmt.Printf("   Content: %s\n\n", result.Content)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// containsKeyError checks if the error message is related to API key issues
func containsKeyError(errorMsg string) bool {
	errorMsgLower := strings.ToLower(errorMsg)
	keyErrorKeywords := []string{"invalid", "key", "auth", "authorization", "unauthorized", "forbidden", "401", "403"}

	for _, keyword := range keyErrorKeywords {
		if strings.Contains(errorMsgLower, keyword) {
			return true
		}
	}
	return false
}

// readCloser is a simple implementation of io.ReadCloser for our use case
type readCloser struct {
	data []byte
	pos  int
}

func (r *readCloser) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *readCloser) Close() error {
	return nil
}