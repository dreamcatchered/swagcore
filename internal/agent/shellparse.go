package agent

import "strings"

// parseCommand разбирает командную строку с поддержкой одинарных и двойных
// кавычек (в стиле sh), чтобы command из манифеста мог содержать сложные выражения.
func parseCommand(s string) []string {
	var args []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	hasArg := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			hasArg = true
		case c == '"' && !inSingle:
			inDouble = !inDouble
			hasArg = true
		case c == '\\' && !inSingle && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			hasArg = true
		case (c == ' ' || c == '\t' || c == '\n') && !inSingle && !inDouble:
			if cur.Len() > 0 || hasArg {
				args = append(args, cur.String())
				cur.Reset()
				hasArg = false
			}
		default:
			cur.WriteByte(c)
			hasArg = true
		}
	}
	if cur.Len() > 0 || hasArg {
		args = append(args, cur.String())
	}
	return args
}
