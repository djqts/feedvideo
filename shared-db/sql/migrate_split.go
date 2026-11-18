package sql

import (
	"fmt"
	"strings"
)

// splitMigrationSQL handles the current MySQL scripts including quoted dynamic SQL.
// Separate statements avoid multiStatements on application connections.
func splitMigrationSQL(source string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote byte
	for i := 0; i < len(source); i++ {
		ch := source[i]
		if quote != 0 {
			b.WriteByte(ch)
			if ch == '\\' && i+1 < len(source) {
				i++
				b.WriteByte(source[i])
				continue
			}
			if ch == quote {
				if i+1 < len(source) && source[i+1] == quote {
					i++
					b.WriteByte(source[i])
				} else {
					quote = 0
				}
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			quote = ch
			b.WriteByte(ch)
			continue
		}
		if ch == '#' || (ch == '-' && i+2 < len(source) && source[i+1] == '-' && (source[i+2] == ' ' || source[i+2] == '\t' || source[i+2] == '\r' || source[i+2] == '\n')) {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
			continue
		}
		if ch == '/' && i+1 < len(source) && source[i+1] == '*' {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			if i+2 < len(source) && source[i+2] == '!' {
				return nil, fmt.Errorf("versioned comments unsupported")
			}
			i += end + 3
			b.WriteByte(' ')
			continue
		}
		if ch == ';' {
			if statement := strings.TrimSpace(b.String()); statement != "" {
				out = append(out, statement)
			}
			b.Reset()
			continue
		}
		b.WriteByte(ch)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if statement := strings.TrimSpace(b.String()); statement != "" {
		out = append(out, statement)
	}
	return out, nil
}
