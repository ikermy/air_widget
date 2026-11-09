package repository

import (
	"air_widget/internal/domain"
	"encoding/json"

	"github.com/ikermy/air-common/v2/pkg/comdb"
)

// InternalRepository внутренние методы работы с БД
type InternalRepository interface {
	GetWidgetBotUser(userID uint64) (domain.WidgetBotData, error)
	GetWidgetBotUsers() ([]domain.WidgetBotData, error)
	ReadResponderName(userID uint64, respId uint64) (json.RawMessage, error)
}

// ExternalDBRepository интерфейс для внешних методов БД (из air-common)
type ExternalDBRepository interface {
	comdb.Exterior
}

// Repository объединяет все репозитории
type Repository struct {
	Internal InternalRepository
	External ExternalDBRepository
}
