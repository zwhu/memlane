package hooks

import (
	"path"
	"strings"
	"unicode"
)

type shellWordToken struct {
	text                string
	separator, redirect bool
}

func shellTokens(command string) []shellWordToken {
	var tokens []shellWordToken
	var word strings.Builder
	var quote rune
	var escaped, started bool
	flush := func() {
		if started {
			tokens = append(tokens, shellWordToken{text: word.String()})
			word.Reset()
			started = false
		}
	}
	for _, ch := range command {
		switch {
		case escaped:
			if quote == '"' && !strings.ContainsRune("$`\"\\\n", ch) {
				word.WriteRune('\\')
			}
			if ch != '\n' {
				word.WriteRune(ch)
				started = true
			}
			escaped = false
		case ch == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
		case ch == '\'' || ch == '"':
			quote, started = ch, true
		case ch == '\n' || strings.ContainsRune(";|&()", ch):
			flush()
			tokens = append(tokens, shellWordToken{separator: true})
		case unicode.IsSpace(ch):
			flush()
		case ch == '>' || ch == '<':
			flush()
			tokens = append(tokens, shellWordToken{redirect: true})
		default:
			word.WriteRune(ch)
			started = true
		}
	}
	flush()
	return tokens
}

func shellPaths(command string) []string {
	var paths []string
	tokens := shellTokens(command)
	for len(tokens) > 0 {
		end := 0
		for end < len(tokens) && !tokens[end].separator {
			end++
		}
		paths = append(paths, commandPaths(tokens[:end])...)
		tokens = tokens[min(end+1, len(tokens)):]
	}
	return paths
}

func commandPaths(words []shellWordToken) []string {
	if len(words) == 0 {
		return nil
	}
	cmd := path.Base(words[0].text)
	search := cmd == "grep" || cmd == "rg" || cmd == "egrep" || cmd == "fgrep"
	code := cmd == "echo" || cmd == "printf" || cmd == "awk" || cmd == "sed"
	var paths []string
	var skip, redirected, patternSet, endOptions bool
	for _, word := range words[1:] {
		value := word.text
		switch {
		case word.redirect:
			redirected = true
		case redirected:
			paths = append(paths, value)
			redirected = false
		case skip:
			skip = false
		case value == "--":
			endOptions = true
		case !endOptions && strings.HasPrefix(value, "-"):
			option, argument, inline := strings.Cut(value, "=")
			switch option {
			case "-e", "--regexp":
				if search {
					skip, patternSet = !inline, true
				} else if cmd == "node" || cmd == "ruby" || cmd == "perl" {
					skip = !inline
				}
			case "-c", "--eval", "--print-code":
				if cmd == "sh" || cmd == "bash" || cmd == "zsh" || cmd == "node" || strings.HasPrefix(cmd, "python") {
					skip = !inline
				}
			case "-g", "--glob", "--iglob", "--max-count", "--context", "--color", "--type", "--type-not":
				skip = search && !inline
			case "-f", "--file":
				patternSet = patternSet || search
				if inline {
					paths = append(paths, argument)
				}
			default:
				if inline {
					paths = append(paths, argument)
				}
			}
		case search && !patternSet:
			patternSet = true
		case code || strings.Contains(value, "://"):
			continue
		default:
			paths = append(paths, value)
		}
	}
	return paths
}
