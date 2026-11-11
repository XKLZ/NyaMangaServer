package organization

import "nyamangaserver.local/server/internal/model"

func cleanLabels(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, model.ErrInput
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		if !validText(v, 128, false) {
			return nil, model.ErrInput
		}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}
