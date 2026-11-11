package search

import (
	"errors"
	"strings"
	"testing"
)

func TestQueryPrecedenceAndUnicode(t *testing.T) {
	n, e := Parse(`日本 OR title:"第１ 卷" artist:ABC* -title_original:"Other"`)
	if e != nil || n.Op != "or" || n.Right.Op != "and" || n.Right.Left.Op != "and" || n.Right.Left.Left.Field != "title" || n.Right.Left.Left.Value != "第1 卷" {
		t.Fatal(n, e)
	}
	for _, q := range []string{`(`, `a OR`, `artist:`, `a)`, `"bad\q"`, strings.Repeat("(", 17) + "a" + strings.Repeat(")", 17), strings.Repeat("a ", 150), strings.Repeat("字", 1025)} {
		if _, e = Parse(q); e == nil {
			t.Fatal("accepted", q)
		}
	}
	_, e = Parse("中文 OR )")
	var syntax *SyntaxError
	if !errors.As(e, &syntax) || syntax.Position != 6 {
		t.Fatal(e)
	}
	n, e = Parse(`"a\"b"`)
	if e != nil || n.Value != `a"b` {
		t.Fatal(n, e)
	}
}
