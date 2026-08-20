package widget

import (
	"air_widget/internal/metrics"
	"fmt"

	"github.com/ikermy/air_common/pkg/model"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// IsOperatorMode возвращает текущий флаг оператора для диалога
func (b *Bot) IsOperatorMode(dialogId uint64) bool {
	_, ok := b.user.operatorModeByDialog.Load(dialogId)
	return ok
}

func (u *User) SetOperatorMode(dialogId uint64, operator bool) {
	if operator {
		u.operatorModeByDialog.Store(dialogId, true)
	} else {
		u.operatorModeByDialog.Delete(dialogId)
	}
	metrics.TrackOperatorModeDialogs(0, u.operatorModeDialogsCount())
}

func (u *User) operatorModeDialogsCount() int {
	count := 0
	u.operatorModeByDialog.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// DisableOperatorMode отключает режим оператора и уведомляет AI-модель
func (u *User) DisableOperatorMode(userId uint32, dialogId uint64, silent ...bool) error {
	// Определяем значение silent (по умолчанию false)
	isSilent := false
	if len(silent) > 0 {
		isSilent = silent[0]
	}

	// 1. Выключаем режим оператора
	u.SetOperatorMode(dialogId, false)
	logger.Debug("Выключен режим оператора для диалога %d", dialogId, userId)

	// 2. Находим respId по dialogId
	respId, err := u.mod.GetRespIdByDialogID(dialogId)
	if err != nil {
		logger.Error("Не удалось найти respId для dialogId %d: %v", dialogId, err)
		return err // Если не нашли, то и сообщение отправить не сможем
	}

	// 3. Получаем экземпляр бота
	value, ok := u.bot.Load(userId)
	if !ok {
		logger.Error("Бот для userId %d не найден, не могу отправить сообщение", userId)
		return fmt.Errorf("бот не найден")
	}
	bot := value.(*Bot)
	if bot == nil {
		logger.Error("Бот для userId %d nil, не могу отправить сообщение", userId)
		return fmt.Errorf("бот nil")
	}

	// 4. Отправляем сообщение пользователю только если не silent режим
	if !isSilent {
		messageText := "Оператор отключился. Marusia AI снова с вами!"
		// Для виджета отправка через систему сообщений AI-модели
		usrChForMsg, err := u.mod.GetCh(respId)
		if err != nil {
			logger.Error("Ошибка получения канала для отправки сообщения пользователю %d: %v", respId, err)
		} else {
			systemName := "assist"
			operatorMsg := u.mod.NewMessage(
				model.Operator{SetOperator: false, Operator: false},
				"assist",
				&model.AssistResponse{Message: messageText},
				&systemName,
			)
			if err := u.trySendToRxCh(usrChForMsg, operatorMsg); err != nil {
				logger.Error("Ошибка отправки сообщения о выключении оператора пользователю %d: %v", respId, err)
			}
		}
	}

	// 5. Уведомляем AI-модель о возобновлении работы
	usrCh, err := u.mod.GetCh(respId)
	if err != nil {
		logger.Error("Каналы для respId %d не найдены при отключении оператора: %v", respId, err)
		// Можно попытаться пересоздать сессию, если это необходимо
		return err
	}

	systemName := "assist"
	operatorOffMsg := u.mod.NewMessage(
		model.Operator{SetOperator: false, Operator: false},
		"assist",
		&model.AssistResponse{Message: "Режим оператора отключен, возобновляю работу AI"},
		&systemName,
	)

	if err := u.trySendToRxCh(usrCh, operatorOffMsg); err != nil {
		logger.Error("Не удалось отправить системное сообщение в модель о выключении оператора: %v", err)
	}

	// 6. Закрываем SSE соединение для оператора, если есть
	if u.op != nil {
		if err := u.op.CloseOperatorSSE(u.ctx, userId, dialogId); err != nil {
			// Логируем ошибку, но не прерываем процесс, так как это не критично
			logger.Error("Не удалось закрыть SSE сессию оператора: %v", err)
		}
	}

	return nil
}

// trySendToRxCh пытается отправить сообщение в RxCh безопасным методом
func (u *User) trySendToRxCh(usrCh *model.Ch, msg model.Message) error {
	if err := usrCh.SendToRx(msg); err != nil {
		return fmt.Errorf("не удалось отправить сообщение в RxCh: %w", err)
	}
	return nil
}
