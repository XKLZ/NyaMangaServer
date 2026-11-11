package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

// This mask covers the legacy remote projection as well as full dictionary
// lookups. Manual rows never pass through this filter.
type dictionaryRead struct {
	queryer
	disabled bool
}

func (q dictionaryRead) QueryContext(ctx context.Context, sql string, args ...any) (*sql.Rows, error) {
	if q.disabled {
		sql = strings.ReplaceAll(sql, "LEFT JOIN tag_translations r ON", "LEFT JOIN tag_translations r ON 0=1 AND")
	}
	return q.queryer.QueryContext(ctx, sql, args...)
}
func maskedDictionary(ctx context.Context, q queryer) (queryer, error) {
	enabled, e := enhancementEnabled(ctx, q, model.EnhancementDictionary)
	return dictionaryRead{q, !enabled}, e
}
func offlineDictionaryGate(ctx context.Context, q queryer) error {
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='enhancement_settings'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	var ok bool
	if e := scopeRow(ctx, q, "SELECT enabled=1 AND state IN('stable','enabling') FROM enhancement_settings WHERE feature='chinese_dictionary'").Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return model.ErrEnhancementDisabled
	}
	return nil
}
func (s *Store) DictionaryOfflineAllowed(ctx context.Context) error {
	return offlineDictionaryGate(ctx, s.Read)
}
func (s *Store) DictionaryResourceState(ctx context.Context) (string, error) {
	_, state, e := activeDictionary(ctx, s.Read)
	return state, e
}
