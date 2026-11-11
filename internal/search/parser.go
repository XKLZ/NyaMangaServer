// Package search owns the bounded query language and saved-search validation.
package search

import (
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"strings"
	"unicode"
	"unicode/utf8"
)

type SyntaxError struct {
	Position int
	Message  string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("查询第 %d 个字符：%s", e.Position+1, e.Message)
}

type token struct {
	value string
	pos   int
	kind  string
}
type parser struct {
	tokens              []token
	index, nodes, depth int
}

func Parse(s string) (*model.SearchNode, error) {
	if !utf8.ValidString(s) || len(s) > 4096 || utf8.RuneCountInString(s) > 1024 {
		return nil, &SyntaxError{0, "查询超出长度限制"}
	}
	r := []rune(s)
	ts := []token{}
	for i := 0; i < len(r); {
		if unicode.IsSpace(r[i]) {
			i++
			continue
		}
		start := i
		if strings.ContainsRune("()-", r[i]) {
			ts = append(ts, token{string(r[i]), i, string(r[i])})
			i++
			continue
		}
		var b strings.Builder
		quoted := false
		for i < len(r) && (!unicode.IsSpace(r[i]) && !strings.ContainsRune("()", r[i])) {
			if r[i] == '"' {
				quoted = true
				i++
				closed := false
				for i < len(r) {
					if r[i] == '"' {
						i++
						closed = true
						break
					}
					if r[i] == '\\' {
						i++
						if i == len(r) || (r[i] != '"' && r[i] != '\\') {
							return nil, &SyntaxError{i, "仅支持引号和反斜线转义"}
						}
					}
					b.WriteRune(r[i])
					i++
				}
				if !closed {
					return nil, &SyntaxError{start, "引号未闭合"}
				}
			} else {
				b.WriteRune(r[i])
				i++
			}
		}
		v := b.String()
		kind := "word"
		if !quoted && (v == "AND" || v == "OR") {
			kind = v
		}
		if v == "" {
			return nil, &SyntaxError{start, "条件不能为空"}
		}
		ts = append(ts, token{v, start, kind})
	}
	ts = append(ts, token{"", len(r), "end"})
	p := parser{tokens: ts}
	if len(ts) == 1 {
		return nil, nil
	}
	n, e := p.or()
	if e == nil && p.peek().kind != "end" {
		e = p.err("多余条件或右括号")
	}
	return n, e
}
func (p *parser) peek() token        { return p.tokens[p.index] }
func (p *parser) err(m string) error { return &SyntaxError{p.peek().pos, m} }
func (p *parser) node(n *model.SearchNode) (*model.SearchNode, error) {
	p.nodes++
	if p.nodes > 256 {
		return nil, p.err("条件节点超过256个")
	}
	return n, nil
}
func (p *parser) or() (*model.SearchNode, error) {
	n, e := p.and()
	for e == nil && p.peek().kind == "OR" {
		p.index++
		r, x := p.and()
		if x != nil {
			return nil, x
		}
		n, e = p.node(&model.SearchNode{Op: "or", Left: n, Right: r})
	}
	return n, e
}
func (p *parser) and() (*model.SearchNode, error) {
	n, e := p.unary()
	for e == nil {
		k := p.peek().kind
		if k == "end" || k == ")" || k == "OR" {
			break
		}
		if k == "AND" {
			p.index++
		}
		r, x := p.unary()
		if x != nil {
			return nil, x
		}
		n, e = p.node(&model.SearchNode{Op: "and", Left: n, Right: r})
	}
	return n, e
}
func (p *parser) unary() (*model.SearchNode, error) {
	t := p.peek()
	if t.kind == "-" || t.kind == "(" {
		p.depth++
		if p.depth > 16 {
			return nil, p.err("嵌套超过16层")
		}
		defer func() { p.depth-- }()
		p.index++
		if t.kind == "-" {
			n, e := p.unary()
			if e != nil {
				return nil, e
			}
			return p.node(&model.SearchNode{Op: "not", Left: n})
		}
		n, e := p.or()
		if e != nil {
			return nil, e
		}
		if p.peek().kind != ")" {
			return nil, p.err("缺少右括号")
		}
		p.index++
		return n, nil
	}
	if t.kind != "word" {
		return nil, p.err("需要查询条件")
	}
	p.index++
	n := &model.SearchNode{Op: "term", Value: textkey.Normalize(t.value)}
	if f, v, ok := strings.Cut(t.value, ":"); ok {
		if f == "" || v == "" {
			return nil, &SyntaxError{t.pos, "字段或标签不能为空"}
		}
		n.Field = textkey.Normalize(f)
		n.Value = textkey.Normalize(v)
		if f != "title" && f != "title_original" {
			if strings.HasSuffix(n.Value, "*") {
				n.Prefix = true
				n.Value = strings.TrimSuffix(n.Value, "*")
			}
			if n.Value == "" || strings.ContainsRune(n.Value, '*') {
				return nil, &SyntaxError{t.pos, "标签仅支持末尾前缀星号"}
			}
		}
	}
	return p.node(n)
}
