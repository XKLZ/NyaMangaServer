package recommendation

import (
	"reflect"
	"testing"
)

func presentationWorks(titles ...string) []SeriesPresentationWork {
	out := []SeriesPresentationWork{}
	for i, title := range titles {
		out = append(out, SeriesPresentationWork{ID: string(rune('a' + i)), Titles: []string{title}})
	}
	return out
}
func TestSeriesPresentationBaseBeforeBareSequelsAndSourceUntouched(t *testing.T) {
	works := presentationWorks("[Event] Sukumizu Sentai Bikininger 3 (Japanese)", "【Creator (Artist)】Sukumizu Sentai Bikininger", "Sukumizu Sentai Bikininger 2")
	before := []string{works[0].Titles[0], works[1].Titles[0], works[2].Titles[0]}
	v := PresentSeries(works, nil)
	if v.Name != "Sukumizu Sentai Bikininger" || v.Basis != "contextual" || v.Uncertain || !reflect.DeepEqual(v.Order, []string{"b", "c", "a"}) {
		t.Fatal(v)
	}
	for i, w := range works {
		if w.Titles[0] != before[i] {
			t.Fatal("input changed")
		}
	}
}
func TestSeriesPresentationExplicitOrdinals(t *testing.T) {
	for _, row := range []struct {
		titles []string
		order  []string
	}{
		{[]string{"Memory 第十卷", "Memory 第１卷", "Memory 第2卷"}, []string{"b", "c", "a"}},
		{[]string{"Memory Vol.2", "Memory Vol.1.5", "Memory Vol.01"}, []string{"c", "b", "a"}},
		{[]string{"Memory 下篇", "Memory 上篇", "Memory 中篇"}, []string{"b", "c", "a"}},
		{[]string{"Memory 后篇", "Memory 前篇"}, []string{"b", "a"}},
		{[]string{"Memory 第2篇", "Memory 第1篇"}, []string{"b", "a"}},
		{[]string{"Memory (第2巻)", "Memory (第1巻)"}, []string{"b", "a"}},
		{[]string{"Memory 第十一卷", "Memory 第十卷", "Memory 第九卷"}, []string{"c", "b", "a"}},
	} {
		v := PresentSeries(presentationWorks(row.titles...), nil)
		if v.Name != "Memory" || v.Basis != "explicit" || v.Uncertain || !reflect.DeepEqual(v.Order, row.order) {
			t.Fatal(row.titles, v)
		}
	}
}
func TestSeriesPresentationConflictsAndMissingPositions(t *testing.T) {
	for _, works := range [][]SeriesPresentationWork{
		presentationWorks("Memory 2", "Memory 2"),
		presentationWorks("Memory 第2卷", "Memory 第3话"),
		presentationWorks("Memory", "Memory", "Memory 2"),
		{{ID: "a", Titles: []string{"Memory Vol.1", "Memory Vol.2"}}, {ID: "b", Titles: []string{"Memory Vol.3"}}},
	} {
		v := PresentSeries(works, nil)
		if !v.Uncertain || v.Basis != "natural" {
			t.Fatal(v)
		}
	}
}
func TestSeriesPresentationNestedBracketsAndEmptyNames(t *testing.T) {
	v := PresentSeries(presentationWorks("[C100] 【Group (Artist)】 Memory 第2卷 （Chinese）", "〔Group〕Memory 第1卷『notes』"), nil)
	if v.Name != "Memory" || !reflect.DeepEqual(v.Order, []string{"b", "a"}) {
		t.Fatal(v)
	}
	for _, titles := range [][]string{{"[Only (annotation)]", "《Entire title》"}, {"[unfinished annotation", "【unfinished"}, {"「Memory」 第2卷", "「Memory」 第3卷"}} {
		v = PresentSeries(presentationWorks(titles...), nil)
		if v.Name != "" || !v.NameRequired {
			t.Fatal(v)
		}
	}
}
func TestSeriesPresentationNamedArcsRequireSharedSubject(t *testing.T) {
	v := PresentSeries(presentationWorks("共同题名 修学旅行篇", "共同题名 海边篇"), nil)
	if v.Name != "共同题名" || v.NameRequired {
		t.Fatal(v)
	}
	v = PresentSeries(presentationWorks("未知作品 修学旅行篇", "另一个作品 海边篇"), nil)
	if !v.NameRequired || v.Name != "" {
		t.Fatal(v)
	}
	v = PresentSeries(presentationWorks("Unknown Story 2", "Other Story 3"), nil)
	if !v.NameRequired {
		t.Fatal("unconfirmed numeric suffix published as series name", v)
	}
}
func TestSeriesPresentationIntrinsicNumbersAndBracketNumbers(t *testing.T) {
	for _, title := range []string{"Agent 007", "3×3", "1984", "Agent 1984"} {
		v := PresentSeries(presentationWorks(title, title), nil)
		if v.Name != title {
			t.Fatal(title, v)
		}
	}
	v := PresentSeries(presentationWorks("Memory (3)", "Memory", "Memory (2)"), nil)
	if v.Name != "Memory" || v.Basis != "contextual" || !reflect.DeepEqual(v.Order, []string{"b", "c", "a"}) {
		t.Fatal(v)
	}
	v = PresentSeries(presentationWorks("Memory (2.5)", "Memory (0.5)"), nil)
	if v.Basis != "contextual" || !reflect.DeepEqual(v.Order, []string{"b", "a"}) {
		t.Fatal(v)
	}
}
func TestSeriesPresentationExistingOrderAndNonPreferredTitles(t *testing.T) {
	works := presentationWorks("Memory Vol.3", "Memory Vol.1", "Memory Vol.2")
	v := PresentSeries(works, []string{"b", "a"})
	if !reflect.DeepEqual(v.Order, []string{"b", "c", "a"}) || v.Uncertain {
		t.Fatal(v)
	}
	v = PresentSeries(works, []string{"a", "b"})
	if !reflect.DeepEqual(v.Order, []string{"a", "b", "c"}) || !v.Uncertain || v.Basis != "existing" {
		t.Fatal(v)
	}
	works = []SeriesPresentationWork{{ID: "a", Titles: []string{"Different alias", "Memory 2"}}, {ID: "b", Titles: []string{"Memory 3"}}}
	v = PresentSeries(works, nil)
	if v.Name != "Memory" || v.Basis != "contextual" {
		t.Fatal(v)
	}
}
