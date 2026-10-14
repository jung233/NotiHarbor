package common

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func SetupGinLog() {
	if *LogDir != "" {
		commonLogPath := filepath.Join(*LogDir, "common.log")
		errorLogPath := filepath.Join(*LogDir, "error.log")
		commonFd, err := os.OpenFile(commonLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open log file")
		}
		errorFd, err := os.OpenFile(errorLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open log file")
		}
		gin.DefaultWriter = io.MultiWriter(os.Stdout, commonFd)
		gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, errorFd)
	}
}

func SysLog(s string) {
	writeSystemLog("info", s, gin.DefaultWriter)
}

func SysError(s string) {
	writeSystemLog("error", s, gin.DefaultErrorWriter)
}

func FatalLog(v ...any) {
	writeSystemLog("fatal", fmt.Sprint(v...), gin.DefaultErrorWriter)
	os.Exit(1)
}

var sensitiveLogPattern = regexp.MustCompile(`(?i)(authorization|cookie|password|client_secret|clientsecret|access_token|token|api_key|session_secret)([\s"':=]+)([^\s,;&"}]+)`)
var authorizationLogPattern = regexp.MustCompile(`(?i)(authorization[\s"':=]+)(?:(?:bearer|qqbot|bot)\s+)?[^\s,;"}]+`)
var cookieLogPattern = regexp.MustCompile(`(?i)((?:set-cookie|cookie)[\s"':=]+)[^\r\n]+`)

func RedactLog(message string) string {
	for _, name := range []string{"SESSION_SECRET", "ENCRYPTION_KEY", "INITIAL_ADMIN_PASSWORD", "SQL_DSN", "REDIS_CONN_STRING"} {
		if value := os.Getenv(name); value != "" {
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
	}
	message = authorizationLogPattern.ReplaceAllString(message, "${1}[REDACTED]")
	message = cookieLogPattern.ReplaceAllString(message, "${1}[REDACTED]")
	return sensitiveLogPattern.ReplaceAllString(message, "${1}${2}[REDACTED]")
}

func writeSystemLog(level, message string, writer io.Writer) {
	entry := map[string]string{"type": "system", "level": level, "time": time.Now().UTC().Format(time.RFC3339Nano), "message": RedactLog(message)}
	if encoded, err := json.Marshal(entry); err == nil {
		_, _ = fmt.Fprintln(writer, string(encoded))
	}
}
