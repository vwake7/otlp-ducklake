// Package sqlguard accepts one SELECT and caps the number of rows the statement can return.
package sqlguard

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Prepare checks sql and returns the statement to run.
// A LIMIT above maxRows is lowered. A missing LIMIT is added.
func Prepare(sql string, maxRows int) (string, error) {
	if maxRows <= 0 {
		return "", fmt.Errorf("max rows must be positive")
	}
	stmt := strings.TrimSpace(sql)
	stmt = strings.TrimSuffix(stmt, ";")
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return "", fmt.Errorf("empty statement")
	}
	tokens, err := scan(stmt)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("empty statement")
	}
	if tokens[0].kind != kindIdent || (tokens[0].upper != "SELECT" && tokens[0].upper != "WITH") {
		return "", fmt.Errorf("only a single SELECT is allowed")
	}
	for _, tok := range tokens {
		if tok.kind == kindIdent && forbidden[tok.upper] {
			return "", fmt.Errorf("statement contains %s", tok.val)
		}
	}
	return applyLimit(stmt, tokens, maxRows), nil
}

var forbidden = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "DROP": true, "ALTER": true,
	"CREATE": true, "COPY": true, "ATTACH": true, "DETACH": true, "INSTALL": true,
	"LOAD": true, "PRAGMA": true, "SET": true, "CALL": true, "EXPORT": true,
	"IMPORT": true, "CHECKPOINT": true, "VACUUM": true, "TRUNCATE": true,
}

const (
	kindIdent  = "ident"
	kindString = "string"
	kindNumber = "number"
	kindOp     = "op"
	kindOther  = "other"
)

type token struct {
	kind  string
	upper string
	val   string
	start int
	end   int
	depth int
}

func scan(sql string) ([]token, error) {
	var out []token
	depth := 0
	i := 0
	for i < len(sql) {
		if sql[i] == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if sql[i] == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			i += 2 + end + 2
			continue
		}
		if unicode.IsSpace(rune(sql[i])) {
			i++
			continue
		}
		if sql[i] == '\'' {
			j, err := skipString(sql, i)
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: kindString, val: sql[i:j], start: i, end: j, depth: depth})
			i = j
			continue
		}
		if sql[i] == '"' {
			j := strings.IndexByte(sql[i+1:], '"')
			if j < 0 {
				return nil, fmt.Errorf("unterminated identifier")
			}
			j = i + 1 + j + 1
			val := sql[i+1 : j-1]
			out = append(out, token{kind: kindIdent, upper: strings.ToUpper(val), val: val, start: i, end: j, depth: depth})
			i = j
			continue
		}
		if sql[i] == '(' {
			depth++
			out = append(out, token{kind: kindOther, val: "(", start: i, end: i + 1, depth: depth})
			i++
			continue
		}
		if sql[i] == ')' {
			out = append(out, token{kind: kindOther, val: ")", start: i, end: i + 1, depth: depth})
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if sql[i] == ';' {
			return nil, fmt.Errorf("only a single SELECT is allowed")
		}
		if op, n := operator(sql[i:]); n > 0 {
			out = append(out, token{kind: kindOp, val: op, start: i, end: i + n, depth: depth})
			i += n
			continue
		}
		if isIdentStart(sql[i]) {
			j := i + 1
			for j < len(sql) && isIdentCont(sql[j]) {
				j++
			}
			val := sql[i:j]
			out = append(out, token{kind: kindIdent, upper: strings.ToUpper(val), val: val, start: i, end: j, depth: depth})
			i = j
			continue
		}
		if sql[i] == '.' || unicode.IsDigit(rune(sql[i])) {
			j := i + 1
			for j < len(sql) && (unicode.IsDigit(rune(sql[j])) || sql[j] == '.') {
				j++
			}
			out = append(out, token{kind: kindNumber, val: sql[i:j], start: i, end: j, depth: depth})
			i = j
			continue
		}
		out = append(out, token{kind: kindOther, val: sql[i : i+1], start: i, end: i + 1, depth: depth})
		i++
	}
	return out, nil
}

func skipString(sql string, i int) (int, error) {
	i++
	for i < len(sql) {
		if sql[i] == '\'' {
			if i+1 < len(sql) && sql[i+1] == '\'' {
				i += 2
				continue
			}
			return i + 1, nil
		}
		i++
	}
	return 0, fmt.Errorf("unterminated string")
}

func operator(s string) (string, int) {
	for _, op := range []string{">=", "<=", "<>", "!=", ">", "<", "="} {
		if strings.HasPrefix(s, op) {
			return op, len(op)
		}
	}
	return "", 0
}

func isIdentStart(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isIdentCont(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func applyLimit(sql string, tokens []token, maxRows int) string {
	for i := len(tokens) - 1; i >= 0; i-- {
		if tokens[i].depth != 0 || tokens[i].upper != "LIMIT" {
			continue
		}
		if i+1 >= len(tokens) {
			break
		}
		next := tokens[i+1]
		if next.upper == "ALL" {
			return sql[:next.start] + strconv.Itoa(maxRows) + sql[next.end:]
		}
		if next.kind != kindNumber {
			break
		}
		n, err := strconv.Atoi(next.val)
		if err != nil || n > maxRows {
			return sql[:next.start] + strconv.Itoa(maxRows) + sql[next.end:]
		}
		return sql
	}
	return sql + " LIMIT " + strconv.Itoa(maxRows)
}
