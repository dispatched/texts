package internal

import "strings"

// mediaExtension maps a stored MIME/content type to a file extension for
// exported media. Falls back to deriving one from the MIME subtype, then to
// ".bin", so an unrecognized type never blocks the export.
func mediaExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic", "image/heif":
		return ".heic"
	case "image/bmp":
		return ".bmp"
	case "video/mp4":
		return ".mp4"
	case "video/3gpp", "video/3gpp2":
		return ".3gp"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/amr", "audio/amr-wb":
		return ".amr"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	default:
		sub := contentType
		if idx := strings.LastIndex(contentType, "/"); idx != -1 && idx+1 < len(contentType) {
			sub = contentType[idx+1:]
		}
		sub = strings.SplitN(sub, ";", 2)[0] // strip any "; charset=..." suffix
		sub = strings.TrimPrefix(sub, "x-")
		sub = strings.TrimPrefix(sub, "vnd.")
		sub = strings.TrimSpace(sub)
		if sub != "" {
			return "." + sub
		}
		return ".bin"
	}
}

// sanitizeFilename strips characters that are unsafe in zip entry paths /
// filesystem names, so contact names and phone numbers can double as folder
// names in the media export.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "",
		"?", "",
		"\"", "",
		"<", "",
		">", "",
		"|", "",
	)
	name = strings.Trim(replacer.Replace(name), " .")
	return name
}

// normalizePhoneNumber removes all non-numeric characters except leading +
// and standardizes US phone numbers to include the +1 country code
// This prevents duplicate conversations due to different phone number formatting
func normalizePhoneNumber(phoneNumber string) string {
	if phoneNumber == "" {
		return ""
	}

	// Check if it starts with +
	hasPlus := strings.HasPrefix(phoneNumber, "+")

	// Remove all non-numeric characters
	var result strings.Builder
	for _, ch := range phoneNumber {
		if ch >= '0' && ch <= '9' {
			result.WriteRune(ch)
		}
	}

	normalized := result.String()
	if normalized == "" {
		return ""
	}

	// Standardize US phone numbers
	if !hasPlus {
		// 10 digits without country code - add +1 (US number)
		if len(normalized) == 10 {
			return "+1" + normalized
		}
		// 11 digits starting with 1 - add + (US number with 1 prefix)
		if len(normalized) == 11 && normalized[0] == '1' {
			return "+" + normalized
		}
		// Other lengths without + - keep as is (might be partial/invalid)
		return normalized
	}

	// Already has +, keep it
	return "+" + normalized
}
