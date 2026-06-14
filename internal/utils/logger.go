package utils

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var (
	VerboseLogging = false
	logger         = log.New(os.Stdout, "", 0) // Remove default timestamp, we'll add our own
	currentLevel   = INFO
)

type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Context   map[string]interface{} `json:"context,omitempty"`
}

// SetVerboseLogging sets the global verbose logging flag
func SetVerboseLogging(verbose bool) {
	VerboseLogging = verbose
	if verbose {
		currentLevel = DEBUG
	}
}

// SetLogLevel sets the minimum log level
func SetLogLevel(level LogLevel) {
	currentLevel = level
}

func logWithLevel(level LogLevel, message string, context map[string]interface{}) {
	if level < currentLevel {
		return
	}

	levelStr := map[LogLevel]string{
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
	}[level]

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     levelStr,
		Message:   message,
		Context:   context,
	}

	if jsonData, err := json.Marshal(entry); err == nil {
		logger.Println(string(jsonData))
	} else {
		// Fallback to simple logging if JSON fails
		logger.Printf("[%s] %s %s", levelStr, entry.Timestamp, message)
	}
}

// LogDebug logs debug messages
func LogDebug(message string, context ...map[string]interface{}) {
	var ctx map[string]interface{}
	if len(context) > 0 {
		ctx = context[0]
	}
	logWithLevel(DEBUG, message, ctx)
}

// LogDebugf logs formatted debug messages
func LogDebugf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logWithLevel(DEBUG, message, nil)
}

// LogInfo logs informational messages
func LogInfo(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logWithLevel(INFO, message, nil)
}

// LogInfoWithContext logs informational messages with context
func LogInfoWithContext(message string, context map[string]interface{}) {
	logWithLevel(INFO, message, context)
}

// LogError logs error messages (always shown)
func LogError(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logWithLevel(ERROR, message, nil)
}

// LogErrorWithContext logs error messages with context
func LogErrorWithContext(message string, err error, context map[string]interface{}) {
	if context == nil {
		context = make(map[string]interface{})
	}
	if err != nil {
		context["error"] = err.Error()
	}
	logWithLevel(ERROR, message, context)
}

// LogWarning logs warning messages
func LogWarning(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logWithLevel(WARN, message, nil)
}

// LogSuccess logs success messages
func LogSuccess(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logWithLevel(INFO, message, map[string]interface{}{"status": "success"})
}
