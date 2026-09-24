package domain

import (
	"time"

	"github.com/ikermy/air-common/pkg/comdom"
)

// WidgetBotData представляет данные пользователя с WaUserBot
type WidgetBotData struct {
	Triggers         []string            // Список триггеров из модели ассистента
	Data             string              // Данные scrypt
	Provider         comdom.ProviderType // Тип провайдера: 1=OpenAI, 2=Mistral
	AssistName       string              // Имя ассистента
	AssistantId      string              // Идентификатор ассистента
	MetaAction       string              // Поле MetaAction из модели ассистента
	UserId           uint32              // Идентификатор пользователя
	AskLimit         uint32              // Лимит запросов
	Events           Notifications       // При каких событиях присылать уведомления
	Espero           uint8               // Значение Espero
	WaUserBotEnabled bool                // Флаг включения бота
	Ignore           bool                // Игнорировать сообщения до ответа ассистента
}

// Notifications события уведомлений
type Notifications struct {
	Start  bool
	End    bool
	Target bool
}

// Redis — параметры подключения (заполняются в main.go из env).
type Redis struct {
	RedisAddr     string // REDIS_ADDR (default: "" — Redis отключён)
	RedisPassword string // REDIS_PASSWORD
	RedisDB       int    // REDIS_DB (default: 0)
}

// WidgetConfig is the internal representation of the JSON stored in
// WidgetBotData.Data. It is intentionally not added to WidgetBotData: Data
// remains the single storage field for widget configuration.
type WidgetConfig struct {
	AllowedUrls  []string   `json:"allowedUrls"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	NeverExpires bool       `json:"neverExpires"`
}
