package middleware

// NOT EXECUTED — LOCAL TESTING PROHIBITED. CI uses only mock requests.
import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"message-pusher/common"
	"message-pusher/model"
)

func sessionTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "session-test.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "tester", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, e := db.DB()
		if e == nil {
			_ = sqlDB.Close()
		}
	})
	t.Setenv("SESSION_STORE", "cookie")
	previousSecret := common.SessionSecret
	common.SessionSecret = "test-session-secret-at-least-32-bytes"
	t.Cleanup(func() { common.SessionSecret = previousSecret })
	store, err := NewSessionStore()
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(SessionCookieGuard(), sessions.Sessions(SessionCookieName, store))
	r.POST("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", 1)
		s.Set("username", "tester")
		s.Set("role", 1)
		s.Set("status", 1)
		if err := s.Save(); err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.Status(200)
	})
	r.GET("/api/user/self", UserAuth(), func(c *gin.Context) { c.JSON(200, gin.H{"id": c.GetInt("id")}) })
	return r
}

func TestCookieSessionSurvivesNetworkSwitchAndStoreRestart(t *testing.T) {
	r := sessionTestRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "http://example.test/login", nil))
	if w.Code != 200 || len(w.Result().Cookies()) == 0 {
		t.Fatal("login cookie missing")
	}
	ck := w.Result().Cookies()[0]
	for _, addr := range []string{"192.0.2.1:4567", "[2001:db8::1]:4567", "[::1]:4567"} {
		// Recreate the store with the same secret, as on process restart.
		restarted := sessionTestRouter(t)
		req := httptest.NewRequest("GET", "http://example.test/api/user/self", nil)
		req.RemoteAddr = addr
		req.AddCookie(ck)
		response := httptest.NewRecorder()
		restarted.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("network %s: %d", addr, response.Code)
		}
	}
}

func TestInvalidAndDuplicateCookiesNeverAuthenticate(t *testing.T) {
	r := sessionTestRouter(t)
	for _, value := range []string{"invalid!", "truncated", ""} {
		req := httptest.NewRequest("GET", "http://example.test/api/user/self", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("invalid cookie authenticated: %d", w.Code)
		}
	}
	req := httptest.NewRequest("GET", "http://example.test/api/user/self", nil)
	req.Header.Set("Cookie", "session=one; session=two")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("duplicate cookie accepted")
	}
}

func TestHTTPSCookieOptionsAndUnstableSecret(t *testing.T) {
	t.Setenv("APP_PUBLIC_URL", "https://[2001:db8::1]")
	t.Setenv("COOKIE_SECURE", "")
	options, err := SessionOptions()
	if err != nil || !options.Secure || !options.HttpOnly || options.SameSite != http.SameSiteLaxMode || options.Domain != "" {
		t.Fatalf("unsafe options: %+v %v", options, err)
	}
	r := sessionTestRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "https://example.test/login", nil))
	ck := w.Result().Cookies()[0]
	common.SessionSecret = "different-stable-secret-at-least-32-bytes"
	store, err := NewSessionStore()
	if err != nil {
		t.Fatal(err)
	}
	r2 := gin.New()
	r2.Use(sessions.Sessions(SessionCookieName, store))
	r2.GET("/self", UserAuth(), func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest("GET", "https://example.test/self", nil)
	req.AddCookie(ck)
	response := httptest.NewRecorder()
	r2.ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatal("changed secret must reject old cookie")
	}
}

func TestSessionCSRFCrossOriginRejected(t *testing.T) {
	t.Setenv("APP_PUBLIC_URL", "https://gateway.example")
	r := sessionTestRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "https://gateway.example/login", nil))
	ck := w.Result().Cookies()[0]
	store, err := NewSessionStore()
	if err != nil {
		t.Fatal(err)
	}
	protected := gin.New()
	protected.Use(sessions.Sessions(SessionCookieName, store), SessionCSRF())
	protected.POST("/mutate", func(c *gin.Context) { c.Status(204) })
	for _, origin := range []string{"https://attacker.example", "https://gateway.example:444", "null", ""} {
		req := httptest.NewRequest("POST", "https://gateway.example/mutate", nil)
		req.AddCookie(ck)
		req.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, req)
		if response.Code != 403 {
			t.Fatalf("origin %q accepted", origin)
		}
	}
	req := httptest.NewRequest("POST", "https://gateway.example/mutate", nil)
	req.AddCookie(ck)
	req.Header.Set("Origin", "https://gateway.example")
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, req)
	if response.Code != 204 {
		t.Fatal("valid origin rejected")
	}
}

func TestRedisSessionRoundTripCIOnly(t *testing.T) {
	redisURL := os.Getenv("CI_REDIS_URL")
	if redisURL == "" {
		t.Skip("CI Redis fixture not provided")
	}
	r := sessionTestRouter(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = true
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })
	t.Setenv("SESSION_STORE", "redis")
	t.Setenv("REDIS_CONN_STRING", redisURL)
	store, err := NewSessionStore()
	if err != nil {
		t.Fatal(err)
	}
	r = gin.New()
	r.Use(sessions.Sessions(SessionCookieName, store))
	r.POST("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", 1)
		s.Set("username", "tester")
		s.Set("role", 1)
		s.Set("status", 1)
		if err := s.Save(); err != nil {
			t.Error(err)
			c.Status(500)
			return
		}
		c.Status(200)
	})
	r.GET("/self", UserAuth(), func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "http://gateway.example/login", nil))
	if w.Code != 200 || len(w.Result().Cookies()) == 0 {
		t.Fatal("Redis session login failed")
	}
	req := httptest.NewRequest("GET", "http://gateway.example/self", nil)
	req.RemoteAddr = "[2001:db8::1]:1234"
	req.AddCookie(w.Result().Cookies()[0])
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatal("Redis IPv6 session read failed")
	}
}
