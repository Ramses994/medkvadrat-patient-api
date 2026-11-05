package good

import (
	"database/sql"
	"time"
)

const mssqlReminderBoundLayout = "2006-01-02T15:04:05"

func stringBinding() sql.NamedArg {
	bound := time.Now().Format(mssqlReminderBoundLayout)
	return sql.Named("now", bound)
}
