package domain

import "github.com/ikermy/air-common/pkg/comdom"

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
