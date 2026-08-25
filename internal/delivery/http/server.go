package http

import (
	"air_widget/internal/metrics"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// WidgetHandlers exposes the widget HTTP handlers without coupling this
// package to the widget implementation.
type WidgetHandlers interface {
	Available() gin.HandlerFunc
	Exam() gin.HandlerFunc
	Validate() gin.HandlerFunc
	Refresh() gin.HandlerFunc
	Username() gin.HandlerFunc
	Dialog() gin.HandlerFunc
	Data() gin.HandlerFunc
	Events() gin.HandlerFunc
	EventsTicket() gin.HandlerFunc
	Code() gin.HandlerFunc
	Enable() gin.HandlerFunc
	Disable() gin.HandlerFunc
	Restart() gin.HandlerFunc
}

type Server struct {
	handlers WidgetHandlers
	engine   *gin.Engine
}

func NewServer(handlers WidgetHandlers) *Server { return &Server{handlers: handlers} }

func uidMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		value := c.Query("uid")
		uid, err := strconv.ParseUint(value, 10, 32)
		if value == "" || err != nil {
			logger.Warn("uidMiddleware: некорректный uid %q", value)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid uid"})
			c.Abort()
			return
		}
		c.Set("uid", uint32(uid))
		c.Next()
	}
}

func (s *Server) Handler() *gin.Engine {
	g := gin.Default()
	g.Use(httpMetricsMiddleware())
	g.GET("/metrics", gin.WrapH(metrics.Handler()))
	g.Use(corsMiddleware())
	v1 := g.Group("/v1")
	w := v1.Group("/widget")
	w.GET("/available", s.handlers.Available())
	w.POST("/exam", s.handlers.Exam())
	w.GET("/validate", s.handlers.Validate())
	w.POST("/refresh", s.handlers.Refresh())
	w.GET("/username", s.handlers.Username())
	w.GET("/dialog", s.handlers.Dialog())
	w.POST("/data", s.handlers.Data())
	w.GET("/events", s.handlers.Events())
	w.POST("/events-ticket", s.handlers.EventsTicket())
	w.Group("", uidMiddleware()).POST("/code", s.handlers.Code())
	airorc := g.Group("/widget", uidMiddleware())
	airorc.GET("/enable", s.handlers.Enable())
	airorc.GET("/disable", s.handlers.Disable())
	airorc.GET("/restart", s.handlers.Restart())
	s.engine = g
	return g
}

func (s *Server) ListenAndServe(addr string) error {
	logger.Infoln("Web server AiR_Widget started")
	return s.Handler().Run(addr)
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if o := c.GetHeader("Origin"); o != "" {
			c.Header("Access-Control-Allow-Origin", o)
		} else {
			c.Header("Access-Control-Allow-Origin", "*")
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Origin, X-Requested-With")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
func httpMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		route = metrics.NormalizeRoute(route)
		metrics.HTTPSRequests.WithLabelValues(c.Request.Method, route, strconv.Itoa(c.Writer.Status())).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
	}
}
