package main

import (
	"context"
	"embed"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"message-pusher/channel"
	"message-pusher/common"
	"message-pusher/gateway"
	"message-pusher/middleware"
	"message-pusher/model"
	"message-pusher/router"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web/build
var buildFS embed.FS

//go:embed web/build/index.html
var indexPage []byte

func main() {
	common.ParseCommandLine()
	if len(common.SessionSecret) < 32 {
		common.FatalLog("SESSION_SECRET must contain at least 32 bytes")
	}
	if err := common.ValidateEncryptionKey(); err != nil {
		common.FatalLog(err)
	}
	if _, err := middleware.SessionOptions(); err != nil {
		common.FatalLog(err)
	}
	common.SetupGinLog()
	common.SysLog("NotiHarbor " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	// Initialize SQL Database
	err := model.InitDB()
	if err != nil {
		common.FatalLog(err)
	}
	if err := model.MigrateStoredCredentials(model.DB); err != nil {
		common.FatalLog("credential encryption migration failed")
	}
	defer func() {
		err := model.CloseDB()
		if err != nil {
			common.FatalLog(err)
		}
	}()

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		common.FatalLog(err)
	}

	// Initialize options
	model.InitOptionMap()

	// Initialize token store
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	channel.TokenStoreInitContext(ctx)
	if err := model.MigrateGateway(model.DB); err != nil {
		common.FatalLog("gateway migration failed")
	}
	workerConcurrency := 4
	if value := os.Getenv("WORKER_CONCURRENCY"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 128 {
			common.FatalLog("WORKER_CONCURRENCY must be between 1 and 128")
		}
		workerConcurrency = parsed
	}
	gatewayDone := make(chan struct{})
	retentionDone := make(chan struct{})
	if common.RedisEnabled {
		service := gateway.NewService(model.DB, common.RDB, gateway.SenderFunc(gatewaySender), gateway.Options{Concurrency: workerConcurrency})
		gateway.SetDefaultService(service)
		attemptDays := retentionDays("DELIVERY_LOG_RETENTION_DAYS", 90)
		auditDays := retentionDays("AUDIT_LOG_RETENTION_DAYS", 365)
		go func() {
			defer close(retentionDone)
			service.RunLogRetention(ctx, attemptDays, auditDays)
		}()
		go func() {
			defer close(gatewayDone)
			if err := service.Run(ctx); err != nil && ctx.Err() == nil {
				common.SysError("gateway worker stopped: " + err.Error())
				stop()
			}
		}()
	} else {
		close(gatewayDone)
		close(retentionDone)
	}
	legacyWorkers := channel.StartLegacyWorkers(ctx)
	go channel.LoadAsyncMessagesContext(ctx)

	// Initialize HTTP server
	server := gin.New()
	server.Use(middleware.RequestLog(), middleware.SafeRecovery())
	var trustedProxies []string
	if configured := os.Getenv("TRUSTED_PROXIES"); configured != "" {
		trustedProxies = strings.Split(configured, ",")
	}
	if err := server.SetTrustedProxies(trustedProxies); err != nil {
		common.FatalLog(err)
	}
	server.Use(middleware.RequestBodyLimit(), middleware.SessionCookieGuard())
	server.SetHTMLTemplate(common.LoadTemplate())
	//server.Use(gzip.Gzip(gzip.DefaultCompression))  // conflict with sse

	// Initialize session store
	store, err := middleware.NewSessionStore()
	if err != nil {
		common.FatalLog(err)
	}
	server.Use(sessions.Sessions(middleware.SessionCookieName, store), middleware.SessionCSRF())

	router.SetRouter(server, buildFS, indexPage)
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}
	httpServer := &http.Server{Addr: ":" + port, Handler: server, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- httpServer.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			common.SysError("HTTP server failed: " + err.Error())
		}
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		common.SysError("HTTP graceful shutdown exceeded deadline")
		_ = httpServer.Close()
	}
	workersDone := make(chan struct{})
	go func() { legacyWorkers.Wait(); close(workersDone) }()
	select {
	case <-workersDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-gatewayDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-retentionDone:
	case <-shutdownCtx.Done():
	}
	if common.RDB != nil {
		if err := common.RDB.Close(); err != nil {
			common.SysError("Redis close failed")
		}
	}
}

func retentionDays(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 || days > 36500 {
		common.FatalLog(name + " must be between 0 and 36500 days")
	}
	return days
}
