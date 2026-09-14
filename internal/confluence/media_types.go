package confluence

import (
	"path"
	"strings"
)

// mediaTypeDescription names a file's kind the way Confluence describes it to
// readers, from its media type and, when the type is generic, its extension.
func mediaTypeDescription(mediaType, filename string) string {
	mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	switch mediaType {
	case "image/png":
		return "PNG Image"
	case "image/jpeg", "image/jpg":
		return "JPEG Image"
	case "image/gif":
		return "GIF Image"
	case "image/svg+xml":
		return "SVG Image"
	case "image/webp":
		return "WebP Image"
	case "application/pdf":
		return "PDF Document"
	case "text/plain":
		return "Text File"
	case "text/csv":
		return "CSV File"
	case "text/html":
		return "HTML Document"
	case "application/json":
		return "JSON File"
	case "application/xml", "text/xml":
		return "XML File"
	case "application/zip", "application/x-zip-compressed":
		return "ZIP Archive"
	case "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return "Word Document"
	case "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "Excel Spreadsheet"
	case "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return "PowerPoint Presentation"
	}
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return "Image"
	case strings.HasPrefix(mediaType, "video/"):
		return "Video"
	case strings.HasPrefix(mediaType, "audio/"):
		return "Audio"
	case strings.HasPrefix(mediaType, "text/"):
		return "Text File"
	}
	switch strings.ToLower(path.Ext(filename)) {
	case ".pdf":
		return "PDF Document"
	case ".docx", ".doc":
		return "Word Document"
	case ".xlsx", ".xls":
		return "Excel Spreadsheet"
	case ".pptx", ".ppt":
		return "PowerPoint Presentation"
	case ".zip":
		return "ZIP Archive"
	}
	return "File"
}
