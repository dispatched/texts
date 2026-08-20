package internal

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// getUserDB is a helper function to get the user's database connection from the context
func getUserDB(c echo.Context) (*sql.DB, error) {
	userID, ok := c.Get("user_id").(string)
	if !ok {
		return nil, fmt.Errorf("user_id not found in context")
	}
	username, ok := c.Get("username").(string)
	if !ok {
		return nil, fmt.Errorf("username not found in context")
	}
	return GetUserDB(userID, username)
}

func HandleUpload(c echo.Context) error {
	// Use a smaller memory limit for the form parsing itself (32 MB)
	// Large files will be streamed directly to disk
	err := c.Request().ParseMultipartForm(32 << 20) // 32 MB max in memory
	if err != nil {
		slog.Error("Error parsing form", "error", err)
		return c.JSON(http.StatusBadRequest, UploadResponse{
			Success: false,
			Error:   "Failed to parse form data. File may be too large or corrupted.",
		})
	}

	file, header, err := c.Request().FormFile("file")
	if err != nil {
		slog.Error("Error getting file", "error", err)
		return c.JSON(http.StatusBadRequest, UploadResponse{
			Success: false,
			Error:   "Failed to get file from form",
		})
	}
	defer file.Close()

	slog.Info("Receiving file", "filename", header.Filename, "size", header.Size)

	// Save uploaded file to temporary location first
	tempFilePath, err := SaveUploadedFile(file, header.Filename)
	if err != nil {
		slog.Error("Error saving file", "error", err)
		return c.JSON(http.StatusInternalServerError, UploadResponse{
			Success: false,
			Error:   "Failed to save uploaded file: " + err.Error(),
		})
	}

	slog.Info("File saved", "path", tempFilePath)

	// Get user ID from context
	userID, ok := c.Get("user_id").(string)
	if !ok {
		return c.JSON(http.StatusUnauthorized, UploadResponse{
			Success: false,
			Error:   "User not authenticated",
		})
	}

	// Get username from context
	username, ok := c.Get("username").(string)
	if !ok {
		return c.JSON(http.StatusUnauthorized, UploadResponse{
			Success: false,
			Error:   "User not authenticated",
		})
	}

	// Start background processing with user context
	go ProcessUploadedFile(userID, username, tempFilePath)

	// Return immediately - client will poll /api/progress for status
	return c.JSON(http.StatusOK, UploadResponse{
		Success:      true,
		MessageCount: 0,
		CallLogCount: 0,
		Processing:   true,
	})
}

func HandleConversations(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	var startDate, endDate *time.Time

	if startStr := c.QueryParam("start"); startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err == nil {
			startDate = &t
		}
	}

	if endStr := c.QueryParam("end"); endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			endDate = &t
		}
	}

	conversations, err := GetConversations(userDB, startDate, endDate)
	if err != nil {
		slog.Error("Error getting conversations", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get conversations",
		})
	}

	return c.JSON(http.StatusOK, conversations)
}

func HandleMessages(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	address := c.QueryParam("address")
	convType := c.QueryParam("type")
	if address == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Address parameter required",
		})
	}

	var startDate, endDate *time.Time

	if startStr := c.QueryParam("start"); startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err == nil {
			startDate = &t
		}
	}

	if endStr := c.QueryParam("end"); endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			endDate = &t
		}
	}

	// If type is "call", return call logs instead of messages
	if convType == "call" {
		calls, err := GetCallLogs(userDB, address, startDate, endDate)
		if err != nil {
			slog.Error("Error getting call logs", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to get call logs",
			})
		}
		return c.JSON(http.StatusOK, calls)
	}

	// If type is "conversation", return combined messages and calls
	if convType == "conversation" {
		// Get user ID from context to fetch settings
		userID, ok := c.Get("user_id").(string)
		if !ok {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "User not authenticated",
			})
		}

		// Fetch user settings
		settings, err := GetUserSettings(userID)
		if err != nil {
			slog.Error("Error getting user settings", "error", err)
			settings = GetDefaultSettings()
		}

		// Use user's configured limit as default, allow query param override
		limit := settings.Conversations.MessageLimit
		if limit <= 0 {
			limit = 100000
		}
		offset := 0

		if limitStr := c.QueryParam("limit"); limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
				limit = parsedLimit
			}
		}

		if offsetStr := c.QueryParam("offset"); offsetStr != "" {
			if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
				offset = parsedOffset
			}
		}

		total, err := CountActivityByAddress(userDB, address, startDate, endDate)
		if err != nil {
			slog.Error("Error counting activity", "error", err)
			total = 0
		}

		activities, err := GetActivityByAddress(userDB, address, startDate, endDate, limit, offset)
		if err != nil {
			slog.Error("Error getting activity", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to get activity",
			})
		}

		// Filter out calls if show_calls setting is false
		if !settings.Conversations.ShowCalls {
			filteredActivities := []ActivityItem{}
			for _, activity := range activities {
				if activity.Type != "call" {
					filteredActivities = append(filteredActivities, activity)
				}
			}
			activities = filteredActivities
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"items": activities,
			"total": total,
		})
	}

	messages, err := GetMessages(userDB, address, startDate, endDate)
	if err != nil {
		slog.Error("Error getting messages", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get messages",
		})
	}

	return c.JSON(http.StatusOK, messages)
}

// HandleMediaItems returns only media (images/videos) for a conversation
func HandleMediaItems(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	address := c.QueryParam("address")
	var startDate, endDate *time.Time

	if startStr := c.QueryParam("start"); startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err == nil {
			startDate = &t
		}
	}

	if endStr := c.QueryParam("end"); endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			endDate = &t
		}
	}

	mediaItems, err := GetMediaByAddress(userDB, address, startDate, endDate)
	if err != nil {
		slog.Error("Error getting media items", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get media items",
		})
	}

	return c.JSON(http.StatusOK, mediaItems)
}

func HandleActivity(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	var startDate, endDate *time.Time

	if startStr := c.QueryParam("start"); startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err == nil {
			startDate = &t
		}
	}

	if endStr := c.QueryParam("end"); endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			endDate = &t
		}
	}

	// Parse pagination parameters
	limit := 50 // default limit
	offset := 0 // default offset

	if limitStr := c.QueryParam("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}

	if offsetStr := c.QueryParam("offset"); offsetStr != "" {
		if val, err := strconv.Atoi(offsetStr); err == nil {
			offset = val
		}
	}

	activities, err := GetActivity(userDB, startDate, endDate, limit, offset)
	if err != nil {
		slog.Error("Error getting activity", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get activity",
		})
	}

	return c.JSON(http.StatusOK, activities)
}

func HandleCalls(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	var startDate, endDate *time.Time

	if startStr := c.QueryParam("start"); startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err == nil {
			startDate = &t
		}
	}

	if endStr := c.QueryParam("end"); endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			endDate = &t
		}
	}

	// Parse pagination parameters
	limit := 50 // default limit
	offset := 0 // default offset

	if limitStr := c.QueryParam("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}

	if offsetStr := c.QueryParam("offset"); offsetStr != "" {
		if val, err := strconv.Atoi(offsetStr); err == nil {
			offset = val
		}
	}

	calls, err := GetAllCalls(userDB, startDate, endDate, limit, offset)
	if err != nil {
		slog.Error("Error getting calls", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get calls",
		})
	}

	return c.JSON(http.StatusOK, calls)
}

func HandleDateRange(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	minDate, maxDate, err := GetDateRange(userDB)
	if err == ErrNoDateRange {
		// No messages imported yet is a normal state for a new account,
		// not a server error - return empty bounds instead of a 500
		return c.JSON(http.StatusOK, map[string]interface{}{
			"min_date": nil,
			"max_date": nil,
		})
	}
	if err != nil {
		slog.Error("Error getting date range", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get date range",
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"min_date": minDate,
		"max_date": maxDate,
	})
}

func HandleProgress(c echo.Context) error {
	progress := GetUploadProgress()
	if progress == nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"status": "no_upload",
		})
	}

	return c.JSON(http.StatusOK, progress)
}

func HandleMedia(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	// Get message ID from query parameter
	messageID := c.QueryParam("id")
	if messageID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Message ID required",
		})
	}

	// Check if transcode is requested (for videos that browser can't play)
	forceTranscode := c.QueryParam("transcode") == "true"

	// Fetch media from database
	media, contentType, err := GetMessageMedia(userDB, messageID)
	if err != nil {
		slog.Error("Error getting media", "error", err)
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "Media not found",
		})
	}

	if len(media) == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "No media for this message",
		})
	}

	// If transcode is requested and this is a video, try to convert it
	if forceTranscode && strings.HasPrefix(contentType, "video/") {
		slog.Info("Transcode requested for video", "messageID", messageID, "contentType", contentType)
		convertedData, err := convertVideoToMP4(media)
		if err != nil {
			slog.Error("Failed to transcode video", "messageID", messageID, "error", err)
			// Continue with original video if conversion fails
		} else {
			slog.Info("Successfully transcoded video", "messageID", messageID)
			media = convertedData
			contentType = "video/mp4"
		}
	}

	slog.Debug("Serving media", "messageID", messageID, "contentType", contentType, "size", len(media))

	// Set appropriate headers
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000") // Cache for 1 year
	c.Response().Header().Set("Accept-Ranges", "bytes")                    // Enable range requests for video streaming

	// Check for Range header (needed for video playback)
	rangeHeader := c.Request().Header.Get("Range")
	if rangeHeader != "" {
		contentLength := int64(len(media))
		var start, end int64 = 0, contentLength - 1

		slog.Debug("Range request received", "messageID", messageID, "range", rangeHeader, "contentType", contentType, "contentLength", contentLength)

		// Parse range header (e.g., "bytes=0-1023" or "bytes=0-")
		n, _ := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
		if n == 1 {
			// Only start was specified (e.g., "bytes=0-")
			end = contentLength - 1
		} else if n == 0 {
			// Invalid range, return 416 Range Not Satisfiable
			slog.Warn("Invalid range header", "range", rangeHeader)
			c.Response().Header().Set("Content-Range", fmt.Sprintf("bytes */%d", contentLength))
			return c.NoContent(http.StatusRequestedRangeNotSatisfiable)
		}

		// Ensure valid range
		if start < 0 || start >= contentLength || end >= contentLength || start > end {
			slog.Warn("Range out of bounds", "start", start, "end", end, "contentLength", contentLength)
			c.Response().Header().Set("Content-Range", fmt.Sprintf("bytes */%d", contentLength))
			return c.NoContent(http.StatusRequestedRangeNotSatisfiable)
		}

		slog.Debug("Serving range", "start", start, "end", end, "size", end-start+1)

		// Set response headers for partial content
		c.Response().Header().Set("Content-Type", contentType)
		c.Response().Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, contentLength))
		c.Response().Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
		c.Response().WriteHeader(http.StatusPartialContent)

		// Write the requested range
		_, writeErr := c.Response().Write(media[start : end+1])
		return writeErr
	}

	// No range request - serve full content
	c.Response().Header().Set("Content-Length", fmt.Sprintf("%d", len(media)))
	return c.Blob(http.StatusOK, contentType, media)
}

// HandleExportMedia streams every image/video/audio attachment in the
// user's account into a single zip file. Rows are read one at a time and
// written straight into the zip on the HTTP response - nothing is buffered
// server-side, so the export's memory footprint stays flat regardless of
// how much media there is. Files are stored (not deflated), since media is
// already compressed, and kept as the original bytes (no HEIC/video/audio
// conversion), matching what's actually in the backup.
func HandleExportMedia(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	rows, err := GetAllMediaForExport(userDB)
	if err != nil {
		slog.Error("Error querying media for export", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to query media",
		})
	}
	defer rows.Close()

	filename := fmt.Sprintf("sbv-media-export-%s.zip", time.Now().Format("2006-01-02"))
	c.Response().Header().Set(echo.HeaderContentType, "application/zip")
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Response().WriteHeader(http.StatusOK)
	// Flush immediately so the client sees the response headers (and starts
	// recognizing this as a download) right away, rather than waiting for
	// the first batch of entries to accumulate.
	c.Response().Flush()

	zw := zip.NewWriter(c.Response())
	defer zw.Close()
	flatView := c.QueryParam("view") == "all"

	written := 0
	for rows.Next() {
		var id int64
		var address, contactName, mediaType string
		var dateUnix int64
		var mediaData []byte

		if err := rows.Scan(&id, &address, &contactName, &dateUnix, &mediaType, &mediaData); err != nil {
			slog.Error("Error scanning media row for export", "error", err)
			continue
		}
		if len(mediaData) == 0 {
			continue
		}

		date := time.Unix(dateUnix, 0)
		folder := sanitizeFilename(contactName)
		if folder == "" {
			folder = sanitizeFilename(address)
		}
		if folder == "" {
			folder = "unknown"
		}

		// id is unique across the whole table, so this name is guaranteed
		// unique within its folder without any extra dedup bookkeeping.
		entryName := fmt.Sprintf("%s/%s_%d%s", folder, date.Format("2006-01-02"), id, mediaExtension(mediaType))
		if flatView {
			entryName = fmt.Sprintf("%s_%s_%d%s", folder, date.Format("2006-01-02"), id, mediaExtension(mediaType))
		}

		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     entryName,
			Method:   zip.Store,
			Modified: date,
		})
		if err != nil {
			// Once a write to the response fails (client disconnected,
			// broken pipe, etc.), every subsequent entry will fail the
			// same way -- there's no client left to receive them. Stop
			// instead of burning CPU and DB reads churning through the
			// rest of the export for nobody.
			slog.Error("Error creating zip entry, aborting export", "error", err, "name", entryName, "files_written", written)
			return nil
		}
		if _, err := w.Write(mediaData); err != nil {
			slog.Error("Error writing zip entry, aborting export", "error", err, "name", entryName, "files_written", written)
			return nil
		}

		written++
		if written%25 == 0 {
			zw.Flush()
			c.Response().Flush()
		}
	}

	if err := rows.Err(); err != nil {
		slog.Error("Error iterating media rows for export", "error", err)
	}

	slog.Info("Media export complete", "files_written", written, "view", c.QueryParam("view"))
	return nil
}

// HandleClearImportedData clears only the signed-in user's imported backup.
// Authentication data and user settings live outside this database.
func HandleClearImportedData(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	if err := ClearImportedData(userDB); err != nil {
		slog.Error("Error clearing imported data", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to clear imported data",
		})
	}

	slog.Info("Imported data cleared", "user_id", c.Get("user_id"))
	return c.NoContent(http.StatusNoContent)
}

func HandleSearch(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	// Get search query from query parameter
	query := c.QueryParam("q")
	if query == "" {
		return c.JSON(http.StatusOK, []SearchResult{})
	}

	// Get limit from query parameter, default to 100
	limit := 100
	if limitStr := c.QueryParam("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Perform search
	results, err := SearchMessages(userDB, query, limit)
	if err != nil {
		slog.Error("Error searching messages", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Search failed: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, results)
}

// HandleAnalytics returns analytics data for the Summary tab
func HandleAnalytics(c echo.Context) error {
	userDB, err := getUserDB(c)
	if err != nil {
		slog.Error("Error getting user database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get user database",
		})
	}

	var startDate, endDate *time.Time

	if startStr := c.QueryParam("start"); startStr != "" {
		t, err := time.Parse(time.RFC3339, startStr)
		if err == nil {
			startDate = &t
		}
	}

	if endStr := c.QueryParam("end"); endStr != "" {
		t, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			endDate = &t
		}
	}

	// Default to top 10 contacts
	topN := 10
	if topStr := c.QueryParam("top"); topStr != "" {
		if val, err := strconv.Atoi(topStr); err == nil && val > 0 && val <= 50 {
			topN = val
		}
	}

	// Timezone offset in minutes from UTC (e.g. -300 for UTC-5, 330 for UTC+5:30)
	tzOffsetMinutes := 0
	if tzStr := c.QueryParam("tz_offset"); tzStr != "" {
		if val, err := strconv.Atoi(tzStr); err == nil && val >= -840 && val <= 840 {
			tzOffsetMinutes = val
		}
	}

	analytics, err := GetAnalytics(userDB, startDate, endDate, topN, tzOffsetMinutes)
	if err != nil {
		slog.Error("Error getting analytics", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get analytics",
		})
	}

	return c.JSON(http.StatusOK, analytics)
}

// HandleVersion returns the application version
func HandleVersion(c echo.Context) error {
	// Try to read version from version.json file first (Docker builds)
	versionFile := "/app/version.json"
	if data, err := os.ReadFile(versionFile); err == nil {
		var versionData map[string]string
		if err := json.Unmarshal(data, &versionData); err == nil {
			return c.JSON(http.StatusOK, versionData)
		}
	}

	return c.JSON(http.StatusOK, map[string]string{
		"version": "dev",
	})
}
