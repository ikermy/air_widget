package domain

import "time"

const (
	// AuthTokenTTL время жизни токена авторизации сессии виджета (с момента первого открытия кнопкой)
	AuthTokenTTL = 60

	// LongPollingTimeout Настройка timeout для long-polling
	LongPollingTimeout = 10 * time.Minute
)
