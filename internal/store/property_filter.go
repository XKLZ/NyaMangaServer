package store

import (
	"nyamangaserver.local/server/internal/model"
	"strings"
)

// Both physical administration and folded client search use this predicate.
func propertyPredicate(v *model.PropertyFilter) (string, []any) {
	if v == nil {
		return "1", nil
	}
	conditions := []string{}
	args := []any{}
	if l := v.Languages; l != nil {
		or := []string{}
		if len(l.AnyOf) > 0 {
			or = append(or, "EXISTS(SELECT 1 FROM effective_edition_languages pl WHERE pl.edition_id=b.id AND pl.language IN("+placeholders(len(l.AnyOf))+"))")
			for _, s := range l.AnyOf {
				args = append(args, s)
			}
		}
		if l.IncludeUnknown {
			or = append(or, "NOT EXISTS(SELECT 1 FROM effective_edition_languages pl WHERE pl.edition_id=b.id)")
		}
		conditions = append(conditions, "("+strings.Join(or, " OR ")+")")
	}
	for _, p := range []struct {
		column  string
		values  []string
		boolean bool
	}{{"translated", v.Translated, true}, {"color_mode", v.ColorMode, false}, {"censorship", v.Censorship, false}, {"extraneous_ads", v.ExtraneousAds, true}} {
		if len(p.values) == 0 {
			continue
		}
		or := []string{}
		value := "(SELECT " + p.column + " FROM effective_edition_properties pp WHERE pp.edition_id=b.id)"
		for _, s := range p.values {
			if s == "unknown" {
				or = append(or, value+" IS NULL")
			} else {
				or = append(or, value+"=?")
				if p.boolean {
					args = append(args, s == "yes")
				} else {
					args = append(args, s)
				}
			}
		}
		conditions = append(conditions, "("+strings.Join(or, " OR ")+")")
	}
	if len(conditions) == 0 {
		return "1", nil
	}
	return strings.Join(conditions, " AND "), args
}
