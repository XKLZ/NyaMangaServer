package recommendation

import (
	"bytes"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"nyamangaserver.local/server/internal/textkey"
)

// Presentation is deliberately independent of matching/title feature extraction.
const SeriesPresentationVersion = "series-presentation-v2"

type SeriesPresentationWork struct {
	ID     string
	Titles []string // Original title first, then effective title for each Edition.
}
type SeriesPresentation struct {
	Name         string
	NameRequired bool
	Order        []string
	Basis        string
	Uncertain    bool
}
type presentationTitle struct {
	clean, stem, kind, arcBase, arc string
	number                          int64 // Exact hundredths; never a file/metadata mutation.
	explicit, tentative, conflict   bool
}

var presentationSpace = regexp.MustCompile(`\s+`)
var presentationNumber = `([0-9]{1,4}(?:\.[0-9]{1,2})?|[零〇一二三四五六七八九十百千两兩]{1,8})`
var presentationLatin = regexp.MustCompile(`(?i)(?:^|[\s:：\-–—~～])(?:vol(?:ume)?\.?|chapter|ch\.?|part)\s*` + presentationNumber + `\s*$`)
var presentationCJK = regexp.MustCompile(`(?:第\s*)?` + presentationNumber + `\s*(巻|卷|話|话|章|篇|編|编)\s*$`)
var presentationSection = regexp.MustCompile(`(上|中|下|前|後|后)(篇|編|编|巻|卷)\s*$`)
var presentationBare = regexp.MustCompile(`([0-9]{1,3}(?:\.[0-9]{1,2})?)\s*$`)
var presentationBareFull = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]{1,2})?$`)
var presentationRoman = regexp.MustCompile(`(?i)\s+(VIII|VII|III|VI|IV|II|IX|V|X|I)\s*$`)
var presentationArc = regexp.MustCompile(`^(.*?)[\s:：\-–—~～]+([^\s:：\-–—~～]{1,40}(?:篇|編|编))$`)
var presentationArcEnd = regexp.MustCompile(`(?:篇|編|编)$`)

func presentationTrim(s string) string {
	return strings.Trim(presentationSpace.ReplaceAllString(s, " "), " \t\r\n-–—:：~～·・,，/／")
}

// Unclosed opening brackets discard their unfinished annotation, rather than
// reintroducing it as a fallback name. ASCII prose quotes are not brackets.
func presentationUnbracket(raw string) (string, string) {
	open := map[rune]rune{'(': ')', '[': ']', '{': '}', '【': '】', '〔': '〕', '〈': '〉', '《': '》', '「': '」', '『': '』', '≪': '≫'}
	close := map[rune]bool{}
	for _, c := range open {
		close[c] = true
	}
	var text, content strings.Builder
	stack := []rune{}
	tail := ""
	for _, r := range strings.TrimSpace(norm.NFKC.String(raw)) {
		if c, ok := open[r]; ok {
			if len(stack) == 0 {
				content.Reset()
				text.WriteRune(' ')
			}
			stack = append(stack, c)
			continue
		}
		if close[r] {
			if len(stack) > 0 && stack[len(stack)-1] == r {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					tail = content.String()
					text.WriteRune(' ')
				}
			}
			continue
		}
		if len(stack) > 0 {
			content.WriteRune(r)
		} else {
			text.WriteRune(r)
			if !unicode.IsSpace(r) {
				tail = ""
			}
		}
	}
	return presentationTrim(text.String()), presentationTrim(tail)
}

func presentationValue(s string) (int64, bool) {
	if n, e := strconv.ParseFloat(s, 64); e == nil && n >= 0 && n <= 9999 {
		return int64(n*100 + .5), true
	}
	digits := map[rune]int64{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '兩': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int64{'十': 10, '百': 100, '千': 1000}
	if !strings.ContainsAny(s, "十百千") {
		var value int64
		for _, r := range s {
			d, ok := digits[r]
			if !ok {
				return 0, false
			}
			value = value*10 + d
			if value > 9999 {
				return 0, false
			}
		}
		return value * 100, s != ""
	}
	var total, n int64
	lastUnit := int64(10000)
	for _, r := range s {
		if v, ok := digits[r]; ok {
			n = v
		} else if v, ok := units[r]; ok {
			if v >= lastUnit {
				return 0, false
			}
			lastUnit = v
			if n == 0 {
				n = 1
			}
			total += n * v
			n = 0
		} else {
			return 0, false
		}
	}
	return (total + n) * 100, s != "" && total+n <= 9999
}

func presentationBareValue(s string) (int64, bool) {
	integer, _, _ := strings.Cut(s, ".")
	if !presentationBareFull.MatchString(s) || (len(integer) > 1 && strings.HasPrefix(integer, "0")) {
		return 0, false
	}
	return presentationValue(s)
}
func presentationExplicit(s string) (stem, kind string, number int64, ok bool) {
	if m := presentationLatin.FindStringSubmatchIndex(s); m != nil {
		n, valid := presentationValue(s[m[2]:m[3]])
		kind = "volume"
		token := strings.ToLower(s[m[0]:m[2]])
		if strings.Contains(token, "ch") {
			kind = "chapter"
		} else if strings.Contains(token, "part") {
			kind = "part"
		}
		return presentationTrim(s[:m[0]]), kind, n, valid
	}
	if m := presentationCJK.FindStringSubmatchIndex(s); m != nil {
		n, valid := presentationValue(s[m[2]:m[3]])
		kind = "part"
		switch s[m[4]:m[5]] {
		case "巻", "卷":
			kind = "volume"
		case "話", "话", "章":
			kind = "chapter"
		}
		return presentationTrim(s[:m[0]]), kind, n, valid
	}
	if m := presentationSection.FindStringSubmatchIndex(s); m != nil {
		v := map[string]int64{"上": 100, "中": 200, "下": 300, "前": 100, "後": 200, "后": 200}[s[m[2]:m[3]]]
		kind = "section_ud"
		if s[m[2]:m[3]] == "前" || s[m[2]:m[3]] == "後" || s[m[2]:m[3]] == "后" {
			kind = "section_fb"
		}
		return presentationTrim(s[:m[0]]), kind, v, true
	}
	return s, "", 0, false
}
func presentationParse(raw string) presentationTitle {
	clean, tail := presentationUnbracket(raw)
	v := presentationTitle{clean: clean, stem: clean}
	if stem, kind, n, ok := presentationExplicit(clean); ok {
		v.stem = stem
		v.kind = kind
		v.number = n
		v.explicit = true
	}
	if tail != "" && clean != "" {
		if stem, kind, n, ok := presentationExplicit(tail); ok && stem == "" {
			if v.explicit && (v.kind != kind || v.number != n) {
				v.conflict = true
			}
			v.kind = kind
			v.number = n
			v.explicit = true
		} else if n, ok := presentationBareValue(tail); ok {
			if v.explicit && v.number != n {
				v.conflict = true
			}
			if !v.explicit {
				v.kind = "numeric"
				v.number = n
				v.tentative = true
			}
		}
	}
	if !v.explicit && !v.tentative {
		if m := presentationBare.FindStringSubmatchIndex(clean); m != nil && m[0] > 0 {
			token := clean[m[2]:m[3]]
			before, _ := utf8.DecodeLastRuneInString(clean[:m[0]])
			if !unicode.IsDigit(before) && !strings.ContainsRune("xX×*", before) {
				if n, ok := presentationBareValue(token); ok {
					v.stem = presentationTrim(clean[:m[0]])
					v.kind = "numeric"
					v.number = n
					v.tentative = true
				}
			}
		} else if m := presentationRoman.FindStringSubmatchIndex(clean); m != nil {
			v.stem = presentationTrim(clean[:m[0]])
			v.kind = "numeric"
			v.tentative = true
			v.number = map[string]int64{"I": 100, "II": 200, "III": 300, "IV": 400, "V": 500, "VI": 600, "VII": 700, "VIII": 800, "IX": 900, "X": 1000}[strings.ToUpper(clean[m[2]:m[3]])]
		}
	}
	if m := presentationArc.FindStringSubmatch(v.stem); m != nil {
		v.arcBase = presentationTrim(m[1])
		v.arc = m[2]
	}
	return v
}

// PresentSeries never changes input Works, metadata, score or grouping. Missing
// aliases/positions are uncertainty, not evidence for inventing volume numbers.
func PresentSeries(works []SeriesPresentationWork, existing []string) SeriesPresentation {
	views := map[string][]presentationTitle{}
	stems := map[string]map[string]map[string]bool{}
	arcBases := map[string]map[string]map[string]bool{}
	add := func(index map[string]map[string]map[string]bool, base, id, value string) {
		k := textkey.Normalize(base)
		if k == "" {
			return
		}
		if index[k] == nil {
			index[k] = map[string]map[string]bool{}
		}
		if index[k][id] == nil {
			index[k][id] = map[string]bool{}
		}
		index[k][id][value] = true
	}
	for _, w := range works {
		for _, title := range w.Titles {
			if strings.TrimSpace(title) == "" {
				continue
			}
			v := presentationParse(title)
			views[w.ID] = append(views[w.ID], v)
			value := "base"
			if v.explicit || v.tentative {
				value = strconv.FormatInt(v.number, 10)
			}
			add(stems, v.stem, w.ID, value)
			if v.arcBase != "" {
				add(arcBases, v.arcBase, w.ID, v.arc)
			}
		}
	}
	confirmed := func(index map[string]map[string]map[string]bool, k string) bool {
		members := index[k]
		values := map[string]bool{}
		for _, m := range members {
			for v := range m {
				values[v] = true
			}
		}
		return len(members) >= 2 && len(values) >= 2
	}
	type nameChoice struct {
		name          string
		support, rank int
	}
	choices := []nameChoice{}
	cleanKeys := map[string]string{}
	for _, w := range works {
		for rank, v := range views[w.ID] {
			if cleanKeys[w.ID] == "" {
				cleanKeys[w.ID] = v.clean
			}
			name := v.stem
			if v.tentative && !confirmed(stems, textkey.Normalize(v.stem)) {
				// Neither delete a possibly intrinsic number nor publish it as a
				// known series sequence. Prefer another reliable view, else ask.
				continue
			}
			if presentationArcEnd.MatchString(name) && !v.explicit {
				if v.arcBase == "" || !confirmed(arcBases, textkey.Normalize(v.arcBase)) {
					continue
				}
				name = v.arcBase
			}
			if name != "" {
				support := len(stems[textkey.Normalize(name)])
				if a := len(arcBases[textkey.Normalize(name)]); a > support {
					support = a
				}
				choices = append(choices, nameChoice{name, support, rank})
			}
		}
	}
	slices.SortStableFunc(choices, func(a, b nameChoice) int {
		if a.support != b.support {
			return b.support - a.support
		}
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return 0 // Preserve deterministic Work/Edition order and source spelling.
	})
	out := SeriesPresentation{Basis: "natural", Uncertain: true, Order: []string{}}
	if len(choices) > 0 {
		out.Name = choices[0].name
	}
	out.NameRequired = out.Name == ""
	type pos struct {
		value    int64
		kind     string
		inferred bool
	}
	positions := map[string]pos{}
	valid := true
	missing := []string{}
	kinds := map[string]bool{}
	contextual := false
	for _, w := range works {
		values := map[int64]bool{}
		types := map[string]bool{}
		inferred := false
		for _, v := range views[w.ID] {
			if v.conflict {
				valid = false
			}
			if v.explicit || (v.tentative && confirmed(stems, textkey.Normalize(v.stem))) {
				values[v.number] = true
				if v.explicit {
					types[v.kind] = true
				} else {
					inferred = true
				}
			}
		}
		if len(values) > 1 || len(types) > 1 {
			valid = false
		}
		if len(values) == 0 {
			missing = append(missing, w.ID)
			continue
		}
		p := pos{kind: "numeric", inferred: inferred}
		for n := range values {
			p.value = n
		}
		for k := range types {
			p.kind = k
			kinds[k] = true
		}
		positions[w.ID] = p
		contextual = contextual || inferred
	}
	if len(kinds) > 1 {
		valid = false
	}
	if len(missing) == 1 {
		base := missing[0]
		supported := false
		for _, v := range views[base] {
			key := textkey.Normalize(v.clean)
			if !v.explicit && !v.tentative && confirmed(stems, key) && len(stems[key]) == len(works) {
				supported = true
			}
		}
		if supported {
			positions[base] = pos{value: -1, kind: "base", inferred: true}
			contextual = true
		} else {
			valid = false
		}
	} else if len(missing) > 0 {
		valid = false
	}
	seen := map[int64]bool{}
	for _, p := range positions {
		if seen[p.value] {
			valid = false
		}
		seen[p.value] = true
	}
	for _, w := range works {
		out.Order = append(out.Order, w.ID)
	}
	if valid && len(positions) == len(works) {
		slices.SortStableFunc(out.Order, func(a, b string) int {
			if positions[a].value < positions[b].value {
				return -1
			}
			if positions[a].value > positions[b].value {
				return 1
			}
			return strings.Compare(a, b)
		})
		kept := []string{}
		for _, id := range out.Order {
			if slices.Contains(existing, id) {
				kept = append(kept, id)
			}
		}
		if slices.Equal(kept, existing) {
			out.Basis = "explicit"
			if contextual {
				out.Basis = "contextual"
			}
			out.Uncertain = false
			return out
		}
	}
	slices.SortStableFunc(out.Order, func(a, b string) int {
		if n := bytes.Compare(textkey.Natural(cleanKeys[a]), textkey.Natural(cleanKeys[b])); n != 0 {
			return n
		}
		return strings.Compare(a, b)
	})
	if len(existing) > 0 {
		added := []string{}
		for _, id := range out.Order {
			if !slices.Contains(existing, id) {
				added = append(added, id)
			}
		}
		out.Order = append(slices.Clone(existing), added...)
		out.Basis = "existing"
	}
	return out
}
