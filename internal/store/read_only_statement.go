package store

import (
	"fmt"
	"strings"
)

func validateReadOnlyStatement(query string) error {
	if len(query) > readOnlyQueryMaxSQL {
		return fmt.Errorf("query exceeds maximum SQL length of %d bytes", readOnlyQueryMaxSQL)
	}

	keyword, statementEnded, err := inspectSQL(query)
	if err != nil {
		return err
	}
	if !strings.EqualFold(keyword, "SELECT") {
		return fmt.Errorf("only SELECT queries are allowed")
	}
	if statementEnded < len(query) {
		return fmt.Errorf("exactly one SQL statement is allowed")
	}
	return nil
}

// inspectSQL returns the first keyword and the index after all permitted input.
// A terminal semicolon is accepted, but any token following it is rejected.
func inspectSQL(query string) (keyword string, permittedEnd int, err error) {
	const (
		plain = iota
		singleQuoted
		doubleQuoted
		backtickQuoted
		bracketQuoted
		lineComment
		blockComment
	)

	state := plain
	statementEnded := false
	for i := 0; i < len(query); {
		char := query[i]
		switch state {
		case singleQuoted:
			i++
			if char == '\'' {
				if i < len(query) && query[i] == '\'' {
					i++
				} else {
					state = plain
				}
			}
			continue
		case doubleQuoted:
			i++
			if char == '"' {
				if i < len(query) && query[i] == '"' {
					i++
				} else {
					state = plain
				}
			}
			continue
		case backtickQuoted:
			i++
			if char == '`' {
				if i < len(query) && query[i] == '`' {
					i++
				} else {
					state = plain
				}
			}
			continue
		case bracketQuoted:
			i++
			if char == ']' {
				state = plain
			}
			continue
		case lineComment:
			i++
			if char == '\n' || char == '\r' {
				state = plain
			}
			continue
		case blockComment:
			if char == '*' && i+1 < len(query) && query[i+1] == '/' {
				state = plain
				i += 2
			} else {
				i++
			}
			continue
		}

		if char == '-' && i+1 < len(query) && query[i+1] == '-' {
			state = lineComment
			i += 2
			continue
		}
		if char == '/' && i+1 < len(query) && query[i+1] == '*' {
			state = blockComment
			i += 2
			continue
		}
		if isSQLSpace(char) {
			i++
			continue
		}
		if statementEnded {
			return keyword, i, nil
		}
		if char == ';' {
			if keyword == "" {
				return "", i, fmt.Errorf("only SELECT queries are allowed")
			}
			statementEnded = true
			i++
			continue
		}
		if keyword == "" && isSQLIdentifierStart(char) {
			start := i
			for i < len(query) && isSQLIdentifierPart(query[i]) {
				i++
			}
			keyword = query[start:i]
			continue
		}
		switch char {
		case '\'':
			state = singleQuoted
		case '"':
			state = doubleQuoted
		case '`':
			state = backtickQuoted
		case '[':
			state = bracketQuoted
		}
		i++
	}

	if state == singleQuoted || state == doubleQuoted || state == backtickQuoted || state == bracketQuoted || state == blockComment {
		return keyword, len(query), fmt.Errorf("unterminated SQL literal or comment")
	}
	return keyword, len(query), nil
}

func isSQLSpace(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}

func isSQLIdentifierStart(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func isSQLIdentifierPart(char byte) bool {
	return isSQLIdentifierStart(char) || char >= '0' && char <= '9'
}
