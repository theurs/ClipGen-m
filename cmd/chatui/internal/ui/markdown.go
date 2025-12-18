// file: internal/ui/markdown.go
package ui

import (
	"regexp"
	"strings"
)

// ConvertMarkdownToHTML converts basic markdown syntax to HTML
func ConvertMarkdownToHTML(text string) string {
	// Handle the chat history format first (author and timestamps)
	text = convertChatHistoryFormat(text)

	// Convert headers (# Header, ## Header, etc.)
	text = convertHeaders(text)

	// Convert **bold** and __bold__
	text = convertBold(text)

	// Convert *italic* and _italic_
	text = convertItalic(text)

	// Convert ~~strikethrough~~
	text = convertStrikethrough(text)

	// Convert `code` (inline)
	text = convertInlineCode(text)

	// Convert ``` code blocks
	text = convertCodeBlocks(text)

	// Convert > quotes
	text = convertBlockquotes(text)

	// Convert links [text](url)
	text = convertLinks(text)

	// Convert lists
	text = convertLists(text)

	// Convert line breaks
	text = convertLineBreaks(text)

	return text
}

// convertChatHistoryFormat handles the chat history format: "Author [time]:\ntext"
func convertChatHistoryFormat(text string) string {
	// Split text into individual message parts
	parts := strings.Split(text, "\r\n\r\n")

	processedParts := make([]string, 0, len(parts))

	for _, part := range parts {
		// Check if this part looks like a chat message with author and timestamp
		lines := strings.Split(part, "\r\n")
		if len(lines) > 0 {
			// Check for the pattern: "Author [timestamp]:"
			re := regexp.MustCompile(`^(.+?) \[([^\]]+)\]:`)
			if re.MatchString(lines[0]) {
				// This is a chat message - process it specially
				matches := re.FindStringSubmatch(lines[0])
				if len(matches) >= 3 {
					author := matches[1]
					timestamp := matches[2]

					// Get the message content (everything except the first line)
					contentLines := lines[1:]
					content := strings.Join(contentLines, "\r\n")

					// Process the content with other markdown functions
					processedContent := convertMarkdownInContent(content)

					// Format as HTML with proper escaping
					htmlPart := "<div class=\"message\"><strong>" + escapeHTML(author) + " [" + escapeHTML(timestamp) + "]:</strong><br>" + processedContent + "</div>"
					processedParts = append(processedParts, htmlPart)
				} else {
					// Not a recognized chat format, just process as regular markdown
					processedParts = append(processedParts, convertMarkdownInContent(part))
				}
			} else {
				// Not a chat format, just process as regular markdown
				processedParts = append(processedParts, convertMarkdownInContent(part))
			}
		}
	}

	return strings.Join(processedParts, "\n")
}

// convertMarkdownInContent applies markdown conversion to plain content
func convertMarkdownInContent(content string) string {
	result := content

	// Apply markdown conversions to the content
	result = convertHeaders(result)
	result = convertBold(result)
	result = convertItalic(result)
	result = convertStrikethrough(result)
	result = convertInlineCode(result)
	result = convertCodeBlocks(result)
	result = convertBlockquotes(result)
	result = convertLinks(result)
	result = convertLists(result)
	result = convertLineBreaks(result)

	return result
}

func convertHeaders(text string) string {
	// Match headers like # Header, ## Header, etc.
	re := regexp.MustCompile(`(?m)^(\s*)(#{1,6})\s+(.*?)$`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		parts := re.FindStringSubmatch(match)
		level := len(parts[2]) // number of # symbols
		prefix := parts[1]
		content := parts[3]
		return prefix + "<h" + string(rune('0'+level)) + ">" + escapeHTML(content) + "</h" + string(rune('0'+level)) + ">"
	})
}

func convertBold(text string) string {
	// Match **text** or __text__
	re := regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		content := ""
		if submatch[1] != "" {
			content = submatch[1]
		} else if submatch[2] != "" {
			content = submatch[2]
		}
		return "<strong>" + escapeHTML(content) + "</strong>"
	})
}

func convertItalic(text string) string {
	// Match *text* or _text_ but not if surrounded by more asterisks/underscores (like **text** or __text__)
	// First pass - handle *text* style (but not **text**)
	text = regexp.MustCompile(`\*([^\*]+?)\*`).ReplaceAllStringFunc(text, func(match string) string {
		// Check if it's already bold (**text**) by looking for surrounding asterisks
		// If it's **text**, it would have been converted to <strong> already
		submatch := regexp.MustCompile(`\*([^\*]+?)\*`).FindStringSubmatch(match)
		if len(submatch) > 1 && !strings.Contains(match, "**") {
			return "<em>" + escapeHTML(submatch[1]) + "</em>"
		}
		return match
	})

	// Second pass - handle _text_ style (but not __text__)
	text = regexp.MustCompile(`_([^_]+?)_`).ReplaceAllStringFunc(text, func(match string) string {
		// Check if it's already bold (__text__) by looking for surrounding underscores
		submatch := regexp.MustCompile(`_([^_]+?)_`).FindStringSubmatch(match)
		if len(submatch) > 1 && !strings.Contains(match, "__") {
			return "<em>" + escapeHTML(submatch[1]) + "</em>"
		}
		return match
	})

	return text
}

func convertStrikethrough(text string) string {
	// Match ~~text~~
	re := regexp.MustCompile(`~~(.+?)~~`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		return "<s>" + escapeHTML(submatch[1]) + "</s>"
	})
}

func convertInlineCode(text string) string {
	// Match `code`
	re := regexp.MustCompile("`([^`]+)`")
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) > 1 {
			return "<code>" + escapeHTML(submatch[1]) + "</code>"
		}
		return match
	})
}

func convertCodeBlocks(text string) string {
	// Match ```language\n...code...\n``` or ```\n...code...\n```
	re := regexp.MustCompile("(?s)```[a-zA-Z]*\n(.*?)```")
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) > 1 {
			return "<pre><code>" + escapeHTML(submatch[1]) + "</code></pre>"
		}
		return match
	})
}

func convertBlockquotes(text string) string {
	// Match lines starting with > (with optional spaces)
	re := regexp.MustCompile(`(?m)^\s*> (.+)$`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		return "<blockquote>" + escapeHTML(submatch[1]) + "</blockquote>"
	})
}

func convertLinks(text string) string {
	// Match [text](url)
	re := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	return re.ReplaceAllString(text, `<a href="$2">$1</a>`)
}

func convertLists(text string) string {
	// Handle both ordered and unordered lists
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))

	var inUnorderedList bool
	var inOrderedList bool

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for unordered list items
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
			if inOrderedList {
				result = append(result, "</ol>")
				inOrderedList = false
			}

			if !inUnorderedList {
				result = append(result, "<ul>")
				inUnorderedList = true
			}

			content := strings.TrimPrefix(trimmed, "- ")
			content = strings.TrimPrefix(content, "* ")
			content = strings.TrimPrefix(content, "+ ")
			result = append(result, "<li>" + escapeHTML(content) + "</li>")
		} else if orderedListItemRegex.MatchString(trimmed) {
			// Check for ordered list items
			if inUnorderedList {
				result = append(result, "</ul>")
				inUnorderedList = false
			}

			if !inOrderedList {
				result = append(result, "<ol>")
				inOrderedList = true
			}

			matches := orderedListItemRegex.FindStringSubmatch(trimmed)
			if len(matches) >= 2 {
				content := strings.TrimSpace(matches[2]) // The content after the number and period
				result = append(result, "<li>" + escapeHTML(content) + "</li>")
			} else {
				result = append(result, escapeHTML(line))
			}
		} else {
			// Not a list item
			if inUnorderedList {
				result = append(result, "</ul>")
				inUnorderedList = false
			}
			if inOrderedList {
				result = append(result, "</ol>")
				inOrderedList = false
			}

			// Check if the line is empty, otherwise add as paragraph
			if line != "" {
				result = append(result, line)
			}
		}
	}

	// Close any open lists at the end
	if inUnorderedList {
		result = append(result, "</ul>")
	}
	if inOrderedList {
		result = append(result, "</ol>")
	}

	return strings.Join(result, "\n")
}

var orderedListItemRegex = regexp.MustCompile(`^(\d+)\.\s+(.*)`)

func convertLineBreaks(text string) string {
	// Only apply line breaks to plain text, not to HTML that already has formatting
	// If the text already contains HTML tags, don't process line breaks
	if strings.Contains(text, "<") && strings.Contains(text, ">") {
		// This text already has HTML formatting, return as is
		return text
	}

	// For plain text, convert line breaks
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
		if lines[i] != "" {
			lines[i] = escapeHTML(lines[i]) + "<br>"
		}
	}
	result := strings.Join(lines, "")
	// Remove trailing <br> if it exists
	if strings.HasSuffix(result, "<br>") {
		result = strings.TrimSuffix(result, "<br>")
	}
	return result
}

// escapeHTML escapes HTML special characters to prevent XSS and rendering issues
func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	text = strings.ReplaceAll(text, "'", "&#39;")
	return text
}