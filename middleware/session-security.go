package middleware

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"message-pusher/common"
)

const SessionCookieName = "session"

// SessionOptions uses a host-only cookie. Secure is explicit or derived from
// the configured public URL, never from an untrusted forwarding header.
func SessionOptions() (sessions.Options, error) {
	secure := false
	public := os.Getenv("APP_PUBLIC_URL")
	if public != "" {
		u, err := url.Parse(public)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return sessions.Options{}, errors.New("invalid APP_PUBLIC_URL")
		}
		secure = u.Scheme == "https"
	}
	if value := os.Getenv("COOKIE_SECURE"); value != "" {
		if value != "true" && value != "false" {
			return sessions.Options{}, errors.New("COOKIE_SECURE must be true or false")
		}
		if secure && value == "false" {
			return sessions.Options{}, errors.New("HTTPS public URL requires secure cookies")
		}
		secure = value == "true"
	}
	sameSite := http.SameSiteLaxMode
	switch strings.ToLower(os.Getenv("COOKIE_SAMESITE")) {
	case "", "lax":
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		if !secure {
			return sessions.Options{}, errors.New("SameSite=None requires secure cookies")
		}
		sameSite = http.SameSiteNoneMode
	default:
		return sessions.Options{}, errors.New("invalid COOKIE_SAMESITE")
	}
	return sessions.Options{Path: "/", HttpOnly: true, Secure: secure, SameSite: sameSite, MaxAge: 30 * 24 * 3600}, nil
}

func NewSessionStore() (sessions.Store, error) {
	if len(common.SessionSecret) < 32 {
		return nil, errors.New("SESSION_SECRET must contain at least 32 bytes and remain stable across instances")
	}
	options, err := SessionOptions()
	if err != nil {
		return nil, err
	}
	var store sessions.Store
	if common.RedisEnabled && os.Getenv("SESSION_STORE") != "cookie" {
		opt := common.ParseRedisOption()
		if opt.Username != "" || opt.TLSConfig != nil {
			// The legacy redistore cannot represent these URL options safely.
			return nil, errors.New("legacy Redis session store does not support ACL username or TLS; set SESSION_STORE=cookie for these Redis configurations")
		}
		redisStore, initErr := redis.NewStoreWithDB(10, opt.Network, opt.Addr, opt.Password, strconv.Itoa(opt.DB), []byte(common.SessionSecret))
		err = initErr
		if err != nil {
			return nil, errors.New("Redis session store initialization failed")
		}
		if err = redis.SetKeyPrefix(redisStore, "message-pusher:session:"); err != nil {
			return nil, errors.New("Redis session key prefix initialization failed")
		}
		store = redisStore
	} else {
		store = cookie.NewStore([]byte(common.SessionSecret))
	}
	store.Options(options)
	return store, nil
}

// Reject ambiguous same-name cookies before the store chooses an identity.
func SessionCookieGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		count := 0
		for _, ck := range c.Request.Cookies() {
			if ck.Name == SessionCookieName {
				count++
			}
		}
		if count > 1 {
			options, _ := SessionOptions()
			http.SetCookie(c.Writer, &http.Cookie{Name: SessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: options.Secure, SameSite: options.SameSite})
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "ambiguous session cookies; clear cookies and sign in again"})
			return
		}
		c.Next()
	}
}

// Browser session mutations require an exact origin match, including port.
func SessionCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		if (c.Request.Method == http.MethodGet && c.Request.URL.Path != "/api/user/logout" && c.Request.URL.Path != "/api/user/token") || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if sessions.Default(c).Get("id") == nil && c.Request.URL.Path != "/api/user/login" {
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = c.GetHeader("Referer")
		}
		u, err := url.Parse(origin)
		wantScheme := "http"
		if c.Request.TLS != nil {
			wantScheme = "https"
		}
		wantHost := c.Request.Host
		if public := os.Getenv("APP_PUBLIC_URL"); public != "" {
			if p, e := url.Parse(public); e == nil {
				wantScheme, wantHost = p.Scheme, p.Host
			}
		}
		if err != nil || u.Host == "" || !strings.EqualFold(u.Host, wantHost) || u.Scheme != wantScheme {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "request origin verification failed"})
			return
		}
		c.Next()
	}
}

func RequestBodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) { c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20); c.Next() }
}
