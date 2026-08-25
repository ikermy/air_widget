package widget

import (
	httpserver "air_widget/internal/delivery/http"
	"air_widget/internal/metrics"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

type webHandlers struct{ u *User }

func (h webHandlers) Available() gin.HandlerFunc    { return h.u.handleAvailable }
func (h webHandlers) Exam() gin.HandlerFunc         { return h.u.handleExam }
func (h webHandlers) Validate() gin.HandlerFunc     { return h.u.handleTokenValidate }
func (h webHandlers) Refresh() gin.HandlerFunc      { return h.u.handleTokenRefresh }
func (h webHandlers) Username() gin.HandlerFunc     { return h.u.handleGetUsername }
func (h webHandlers) Dialog() gin.HandlerFunc       { return h.u.handleReadDialog }
func (h webHandlers) Data() gin.HandlerFunc         { return h.u.handleData }
func (h webHandlers) Events() gin.HandlerFunc       { return h.u.handleEvents }
func (h webHandlers) EventsTicket() gin.HandlerFunc { return h.u.handleEventsTicket }
func (h webHandlers) Code() gin.HandlerFunc         { return h.u.handleWidgetCode }
func (h webHandlers) Enable() gin.HandlerFunc       { return h.u.enableBot }
func (h webHandlers) Disable() gin.HandlerFunc      { return h.u.disableBot }
func (h webHandlers) Restart() gin.HandlerFunc      { return h.u.restartBot }

func (u *User) WebHook() {
	metrics.Register()
	server := httpserver.NewServer(webHandlers{u: u})
	if err := server.ListenAndServe(":8080"); err != nil {
		logger.Error("ошибка запуска WEB сервера AiR_Widget: %e", err)
	}
}
