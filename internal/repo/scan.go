package repo

import (
	"fmt"
	"strconv"
)

// bitInt scans MSSQL bit/int/smallint (go-mssqldb may yield bool or int64).
type bitInt int

func (b *bitInt) Scan(src any) error {
	n, err := coerceInt(src)
	if err != nil {
		return err
	}
	*b = bitInt(n)
	return nil
}

type nullBitInt struct {
	Int   int
	Valid bool
}

func (n *nullBitInt) Scan(src any) error {
	if src == nil {
		n.Valid = false
		n.Int = 0
		return nil
	}
	v, err := coerceInt(src)
	if err != nil {
		return err
	}
	n.Valid = true
	n.Int = v
	return nil
}

func coerceInt(src any) (int, error) {
	switch v := src.(type) {
	case nil:
		return 0, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case uint8:
		return int(v), nil
	case []byte:
		s := string(v)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("coerceInt []byte: %w", err)
		}
		return n, nil
	case string:
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("coerceInt string: %w", err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("coerceInt: unsupported %T", src)
	}
}
