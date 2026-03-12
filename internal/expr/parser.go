package expr

import (
	"fmt"
	"strings"
	"unicode"
)

// Operator represents a Tekton WhenExpression operator.
type Operator string

const (
	OpIn    Operator = "in"
	OpNotIn Operator = "notin"
)

// Expression represents a parsed `if:` expression.
type Expression struct {
	Operand  string   // e.g., "$(params.branch)" or "$(tasks.check.results.status)"
	Operator Operator // in or notin
	Values   []string // e.g., ["main"] or ["staging", "production"]
}

// Parse parses an `if:` expression string into a structured Expression.
//
// Supported forms:
//
//	params.X == 'value'
//	params.X != 'value'
//	params.X in ("a", "b")
//	params.X not in ("a", "b")
//	tasks.X.results.Y == 'value'
func Parse(input string) (*Expression, error) {
	p := &parser{input: input, pos: 0}
	return p.parse()
}

type parser struct {
	input string
	pos   int
}

func (p *parser) parse() (*Expression, error) {
	p.skipWhitespace()

	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("expected operand, got end of input")
	}

	// Parse operand: params.X or tasks.X.results.Y
	operand, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	p.skipWhitespace()

	// Parse operator.
	op, err := p.parseOperator()
	if err != nil {
		return nil, err
	}

	p.skipWhitespace()

	// Parse values.
	values, err := p.parseValues()
	if err != nil {
		return nil, err
	}

	p.skipWhitespace()
	if p.pos < len(p.input) {
		return nil, fmt.Errorf("unexpected trailing content: %q", p.input[p.pos:])
	}

	return &Expression{
		Operand:  operand,
		Operator: op,
		Values:   values,
	}, nil
}

func (p *parser) parseOperand() (string, error) {
	start := p.pos

	// Read dotted identifier: params.X or tasks.X.results.Y
	for p.pos < len(p.input) {
		ch := rune(p.input[p.pos])
		if ch == '.' || ch == '-' || ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			p.pos++
		} else {
			break
		}
	}

	raw := p.input[start:p.pos]
	if raw == "" {
		return "", fmt.Errorf("expected operand, got %q", p.peekChar())
	}

	// Validate prefix.
	if !strings.HasPrefix(raw, "params.") && !strings.HasPrefix(raw, "tasks.") {
		return "", fmt.Errorf("operand %q must start with 'params.' or 'tasks.'", raw)
	}

	return "$(" + raw + ")", nil
}

func (p *parser) parseOperator() (Operator, error) {
	rest := p.input[p.pos:]

	if strings.HasPrefix(rest, "==") {
		p.pos += 2
		return OpIn, nil
	}
	if strings.HasPrefix(rest, "!=") {
		p.pos += 2
		return OpNotIn, nil
	}
	if strings.HasPrefix(rest, "not in") {
		p.pos += 6
		return OpNotIn, nil
	}
	if strings.HasPrefix(rest, "not ") {
		return "", fmt.Errorf("unsupported operator 'not' (only ==, !=, in, not in are supported)")
	}
	if strings.HasPrefix(rest, "in") {
		p.pos += 2
		return OpIn, nil
	}
	if strings.HasPrefix(rest, "&&") || strings.HasPrefix(rest, "||") {
		return "", fmt.Errorf("unsupported operator %q (only ==, !=, in, not in are supported)", rest[:2])
	}

	return "", fmt.Errorf("expected operator (==, !=, in, not in), got %q", p.peekWord())
}

func (p *parser) parseValues() ([]string, error) {
	p.skipWhitespace()

	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("expected quoted string or value list, got end of input")
	}

	// Check for parenthesized list: ("a", "b")
	if p.input[p.pos] == '(' {
		return p.parseValueList()
	}

	// Single quoted/double-quoted value.
	val, err := p.parseQuotedString()
	if err != nil {
		return nil, err
	}
	return []string{val}, nil
}

func (p *parser) parseValueList() ([]string, error) {
	p.pos++ // skip '('
	p.skipWhitespace()

	if p.pos < len(p.input) && p.input[p.pos] == ')' {
		return nil, fmt.Errorf("empty value list")
	}

	var values []string
	for {
		p.skipWhitespace()
		val, err := p.parseQuotedString()
		if err != nil {
			return nil, err
		}
		values = append(values, val)

		p.skipWhitespace()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("unterminated value list, expected ')'")
		}
		if p.input[p.pos] == ')' {
			p.pos++
			return values, nil
		}
		if p.input[p.pos] == ',' {
			p.pos++
			continue
		}
		return nil, fmt.Errorf("expected ',' or ')' in value list, got %q", string(p.input[p.pos]))
	}
}

func (p *parser) parseQuotedString() (string, error) {
	p.skipWhitespace()
	if p.pos >= len(p.input) {
		return "", fmt.Errorf("expected quoted string, got end of input")
	}

	quote := p.input[p.pos]
	if quote != '\'' && quote != '"' {
		return "", fmt.Errorf("expected quoted string, got %q", string(quote))
	}

	p.pos++ // skip opening quote
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != quote {
		p.pos++
	}
	if p.pos >= len(p.input) {
		return "", fmt.Errorf("unterminated string starting at position %d", start-1)
	}
	val := p.input[start:p.pos]
	p.pos++ // skip closing quote
	return val, nil
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) peekChar() string {
	if p.pos >= len(p.input) {
		return "<EOF>"
	}
	return string(p.input[p.pos])
}

func (p *parser) peekWord() string {
	start := p.pos
	for start < len(p.input) && p.input[start] != ' ' {
		start++
	}
	if start == p.pos {
		return "<EOF>"
	}
	return p.input[p.pos:start]
}
