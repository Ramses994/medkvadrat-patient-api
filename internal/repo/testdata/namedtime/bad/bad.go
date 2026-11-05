package bad

import (
	"database/sql"
	"time"
)

func directTimeBinding(now time.Time) sql.NamedArg { return sql.Named("now", now) }

func localTimeBinding() sql.NamedArg {
	bound := time.Now()
	return sql.Named("now", bound)
}

func returnedTimeBinding() sql.NamedArg { return sql.Named("now", makeTime()) }

func makeTime() time.Time { return time.Now() }

func chainedTimeBinding(now time.Time) sql.NamedArg {
	return sql.Named("now", now.Add(-time.Hour).UTC())
}

type row struct{ At time.Time }

func fieldTimeBinding(r row) sql.NamedArg { return sql.Named("now", r.At) }

func nullTimeBinding(v sql.NullTime) sql.NamedArg { return sql.Named("now", v) }
