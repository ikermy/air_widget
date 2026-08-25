package domain

import (
	"github.com/ikermy/air-common/pkg/mode"
)

var (
	EndDialog = make(chan uint64, 1) // Канал для передачи Id диалога при отключении клиента - для непосредственного сохранения в БД
	UsersDB   = make(chan bool)      // Канал уведомления о завершении операций пользователями ДБ
	Exit      = make(chan bool, 1)   // Канал завершения работы приложения

	// RedisAddr Redis — параметры подключения (заполняются в main.go из env).
	RedisAddr     string // REDIS_ADDR (default: "" — Redis отключён)
	RedisPassword string // REDIS_PASSWORD
	RedisDB       int    // REDIS_DB (default: 0)
)

func init() {
	mode.SetEndDialogChannel(EndDialog)
}
