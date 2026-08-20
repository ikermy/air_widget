package mysql

import (
	"air_widget/internal/domain"
	"air_widget/internal/repository"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air_common/pkg/comdb"
	"github.com/ikermy/air_common/pkg/mode"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_common/pkg/model/create"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// NullBytes промежуточный тип для загрузки массива байт из базы
type NullBytes struct {
	Bytes []byte
	Valid bool // Valid = true, если Bytes не NULL
}

// Implementation реализация интерфейса Implementation для MySQL
type Implementation struct {
	db *comdb.DB
}

// New создаёт новый MySQL репозиторий пользователей
func New(db *comdb.DB) (repository.Repository, error) {
	if db == nil {
		return repository.Repository{}, fmt.Errorf("database connection is nil")
	}

	repo := &Implementation{
		db: db,
	}

	return repository.Repository{
		Internal: repo,
		External: db,
	}, nil
}

// GetWidgetBotUsers получает список пользователей с настройками Widget
// Всегда возвращаем только записи с Widget_enabled = 1
func (i *Implementation) GetWidgetBotUsers() ([]domain.WidgetBotData, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.db.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Всегда возвращаем только записи с Widget_enabled = 1
	query := `
   SELECT
    c.UserId,
    c.Widget,
    c.Widget_enabled,
    u_gpt.Name,
    u_gpt.AssistantId,
    u_gpt.Data,
    um.Provider,
    n.Start,
    n.End,
    n.Target
   FROM
    channels AS c
   LEFT JOIN
    user_models AS um ON c.UserId = um.UserId AND um.IsActive = 1
   LEFT JOIN
    user_gpt AS u_gpt ON um.ModelId = u_gpt.Id
   LEFT JOIN
    notifications AS n ON c.UserId = n.UserId
   WHERE
    c.Widget IS NOT NULL AND c.Widget_enabled = 1`

	rows, err := i.db.Conn().QueryContext(ctx, query)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении пользователей бота: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена при получении пользователей бота: %w", err)
		default:
			return nil, fmt.Errorf("failed to execute stored procedure: %w", err)
		}
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			logger.Info("ошибка закрытия rows: %v", err)
		}
	}(rows)

	// Собираем результаты
	var users []domain.WidgetBotData
	for rows.Next() {
		var user domain.WidgetBotData
		var Name, assistantId sql.NullString // На случай NULL значений
		var provider sql.NullByte            // Провайдер из БД (TINYINT)
		var data NullBytes                   // Для поля Data из BLOB
		var start, end, target sql.NullBool  // Для полей уведомлений
		var widget sql.NullString            // Для поля Widget

		err = rows.Scan(
			&user.UserId,
			&widget,
			&user.WaUserBotEnabled,
			&Name,
			&assistantId,
			&data,
			&provider,
			&start,
			&end,
			&target,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		fillWidgetBotData(&user, widget, Name, assistantId, provider, data, start, end, target, false)

		users = append(users, user)
	}

	// Проверяем ошибки после обработки результатов
	if err = rows.Err(); err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при обработке результатов пользователей бота: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена при обработке результатов пользователей бота: %w", err)
		default:
			return nil, fmt.Errorf("error iterating rows: %w", err)
		}
	}

	return users, nil
}

// GetWidgetBotUser получает информацию о конкретном боте пользователя по userID
func (i *Implementation) GetWidgetBotUser(userID uint32) (domain.WidgetBotData, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.db.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Запрос для получения данных конкретного пользователя
	query := `
   SELECT
    c.UserId,
    c.Widget,
    c.Widget_enabled,
    u_gpt.Name,
    u_gpt.AssistantId,
    u_gpt.Data,
    um.Provider,
    n.Start,
    n.End,
    n.Target
   FROM
    channels AS c
   LEFT JOIN
    user_models AS um ON c.UserId = um.UserId AND um.IsActive = 1
   LEFT JOIN
    user_gpt AS u_gpt ON um.ModelId = u_gpt.Id
   LEFT JOIN
    notifications AS n ON c.UserId = n.UserId
   WHERE
    c.UserId = ? AND c.Widget IS NOT NULL AND c.Widget_enabled = 1`

	var user domain.WidgetBotData
	var Name, assistantId sql.NullString // На случай NULL значений
	var provider sql.NullByte            // Провайдер из БД (TINYINT)
	var data NullBytes                   // Для поля Data из BLOB
	var start, end, target sql.NullBool  // Для полей уведомлений
	var widget sql.NullString            // Для поля Widget

	err := i.db.Conn().QueryRowContext(ctx, query, userID).Scan(
		&user.UserId,
		&widget,
		&user.WaUserBotEnabled,
		&Name,
		&assistantId,
		&data,
		&provider,
		&start,
		&end,
		&target,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return domain.WidgetBotData{}, fmt.Errorf("пользователь с ID %d не найден или бот отключен", userID)
		case errors.Is(err, context.DeadlineExceeded):
			return domain.WidgetBotData{}, fmt.Errorf("тайм-аут (%d с) при получении данных пользователя %d: %w", mode.GetSQLTimeToCancel(), userID, err)
		case errors.Is(err, context.Canceled):
			return domain.WidgetBotData{}, fmt.Errorf("операция отменена при получении данных пользователя %d: %w", userID, err)
		default:
			return domain.WidgetBotData{}, fmt.Errorf("ошибка получения данных пользователя %d: %w", userID, err)
		}
	}

	fillWidgetBotData(&user, widget, Name, assistantId, provider, data, start, end, target, true)

	return user, nil
}

func fillWidgetBotData(user *domain.WidgetBotData, widget, name, assistantID sql.NullString, provider sql.NullByte, data NullBytes, start, end, target sql.NullBool, defaultProvider bool) {
	if widget.Valid {
		user.Data = widget.String
	}
	if assistantID.Valid {
		user.AssistantId = assistantID.String
	}
	if name.Valid {
		user.AssistName = name.String
	}
	if provider.Valid {
		user.Provider = commdom.ProviderType(provider.Byte)
	} else if defaultProvider {
		user.Provider = commdom.ProviderOpenAI
	}
	if data.Valid {
		if mdata, err := create.DecompressModelData(data.Bytes); err == nil {
			user.MetaAction = mdata.MetaAction
			user.Triggers = mdata.Triggers
			user.AskLimit = uint32(mdata.Espero.Limit)
			user.Espero = mdata.Espero.Wait
			user.Ignore = mdata.Espero.Ignore
		}
	}
	if start.Valid {
		user.Events.Start = start.Bool
	}
	if end.Valid {
		user.Events.End = end.Bool
	}
	if target.Valid {
		user.Events.Target = target.Bool
	}
}

func (i *Implementation) ReadResponderName(respId uint64) (json.RawMessage, error) {
	// Ищем имя пользователя и realRespId в кэше
	//if value, found := d.userName.Load(respId); found {
	//	return value.(json.RawMessage), nil
	//}

	// Читаем имя пользователя из базы данных
	//db, err := sql.Open("mysql", d.dsn)
	//if err != nil {
	//	return nil, fmt.Errorf("ошибка открытия базы данных: %w", err)
	//}
	//defer db.Close()

	ctx, cancel := context.WithTimeout(i.db.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var respData sql.NullString
	err := i.db.Conn().QueryRowContext(ctx, "SELECT ReadResponderName(?)", respId).Scan(&respData)
	if err != nil {
		return nil, fmt.Errorf("ошибка вызова хранимой функции ReadDemoUserName: %w", err)
	}

	if !respData.Valid {
		return nil, nil
	}

	// Сохраняем имя пользователя в кэш
	//d.userName.Store(respId, json.RawMessage(respData.String))

	return json.RawMessage(respData.String), nil
}
