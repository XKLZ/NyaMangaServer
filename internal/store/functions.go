package store

import (
	"database/sql/driver"
	"modernc.org/sqlite"
	"nyamangaserver.local/server/internal/textkey"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("nya_normalize_v1", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, _ := args[0].(string)
		return textkey.Normalize(s), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("nya_natural_v1", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, _ := args[0].(string)
		return textkey.Natural(s), nil
	})
}
