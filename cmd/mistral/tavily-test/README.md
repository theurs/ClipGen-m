# Tavily Test Utility

This is a simple utility to test the Tavily API for web search functionality, used as part of the Mistral CLI tools.

## Setup

1. Get your Tavily API key from [Tavily API](https://tavily.com/)
2. Create a configuration file `tavily.conf` in the same directory as `mistral.conf` (usually in `%APPDATA%\clipgen-m\` on Windows)
3. Add your API key to the file in JSON format:

```json
{
  "api_keys": [
    "your_tavily_api_key_here"
  ]
}
```

You can add multiple API keys in the array for rotation in case one reaches its usage limit.

## Features

- Web search functionality using Tavily API
- Support for multiple API keys with automatic rotation on errors
- Summary extraction from search results
- Top results with titles, URLs and content snippets
- Automatic content length limiting to prevent context overload

## Usage

```bash
go run main.go "your search query"
```

Or build and run:

```bash
go build .
./tavily-test "your search query"
```

## Example

```
go run main.go "What is the weather like in New York today?"
```

## Integration

This utility is used as an integral part of the Mistral CLI tool for web search functionality. The Mistral CLI automatically handles search queries when appropriate and integrates search results into the conversation flow.

## Parameters

The tool sends optimized requests with:
- Basic search depth
- Answer inclusion enabled
- Raw content inclusion disabled
- Limited to 3 max results to prevent context overload
- Top 2 results returned to avoid context overload