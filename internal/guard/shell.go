package guard

import (
	"path"
	"slices"
	"strings"
)

// command is one simple command of a shell script: its words (the program
// first, assignments and wrappers removed) and the targets of its redirections.
type command struct {
	words     []string
	redirects []string
}

// wrappers run the command that follows their options. shells run a script
// given with -c.
var (
	wrappers = map[string]bool{"env": true, "sudo": true, "doas": true, "nohup": true, "time": true, "command": true, "exec": true,
		"nice": true, "timeout": true, "setsid": true, "stdbuf": true, "xargs": true, "watch": true, "builtin": true, "ionice": true}
	shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}
)

// maxDepth bounds the recursion into scripts inside scripts.
const maxDepth = 8

// commands splits a shell script into its simple commands. It over-approximates
// on purpose: command substitutions, subshells, `bash -c` scripts and `eval`
// are parsed as scripts of their own, so a forbidden command cannot hide in
// them. It is not a shell: what it cannot see (an interpreter that opens a file
// itself) is left to the check of the diff after the round.
func commands(script string, depth int) []command {
	if depth > maxDepth {
		return nil
	}
	var out []command
	for _, c := range split(script, depth) {
		out = append(out, unwrap(c, depth)...)
	}
	return out
}

// split tokenizes the script into simple commands, honoring quotes and escapes.
func split(script string, depth int) []command {
	var (
		out  []command
		cur  command
		word strings.Builder
		have bool // a word is being built (an empty quoted string counts)
		redo bool // the next word is a redirection target
	)
	flushWord := func() {
		if !have {
			return
		}
		w := word.String()
		word.Reset()
		have = false
		if redo {
			cur.redirects = append(cur.redirects, w)
			redo = false
			return
		}
		cur.words = append(cur.words, w)
	}
	endCommand := func() {
		flushWord()
		if len(cur.words) > 0 || len(cur.redirects) > 0 {
			out = append(out, cur)
		}
		cur = command{}
		redo = false
	}
	rs := []rune(script)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\' && i+1 < len(rs):
			i++
			word.WriteRune(rs[i])
			have = true
		case r == '\'':
			end := indexRune(rs, '\'', i+1)
			word.WriteString(string(rs[i+1 : end]))
			have, i = true, end
		case r == '"':
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) {
					j++
				}
			}
			inner := string(rs[i+1 : min(j, len(rs))])
			out = append(out, substitutions(inner, depth)...)
			word.WriteString(inner)
			have, i = true, min(j, len(rs))
		case r == '`':
			end := indexRune(rs, '`', i+1)
			out = append(out, commands(string(rs[i+1:end]), depth+1)...)
			i = end
		case r == '$' && i+1 < len(rs) && rs[i+1] == '(':
			end := matchParen(rs, i+1)
			out = append(out, commands(string(rs[i+2:end]), depth+1)...)
			word.WriteString("$")
			have, i = true, end
		case r == ' ' || r == '\t':
			flushWord()
		case r == '\n' || r == ';' || r == '&' || r == '|' || r == '(' || r == ')' || r == '{' || r == '}':
			if r == '&' && i+1 < len(rs) && rs[i+1] == '>' { // &> file
				i++
				flushWord()
				redo = true
				continue
			}
			endCommand()
		case r == '>' || r == '<':
			// An fd number glued to the operator (2>file) is not a word.
			if have && isDigits(word.String()) {
				word.Reset()
				have = false
			} else {
				flushWord()
			}
			for i+1 < len(rs) && (rs[i+1] == '>' || rs[i+1] == '|') {
				i++
			}
			if i+1 < len(rs) && rs[i+1] == '&' { // 2>&1: a descriptor, not a file
				i++
				continue
			}
			redo = r == '>'
		case r == '#' && !have:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			endCommand()
		default:
			word.WriteRune(r)
			have = true
		}
	}
	endCommand()
	return out
}

// substitutions parses the $(...) and `...` found inside double quotes.
func substitutions(s string, depth int) []command {
	var out []command
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '$' && i+1 < len(rs) && rs[i+1] == '(':
			end := matchParen(rs, i+1)
			out = append(out, commands(string(rs[i+2:end]), depth+1)...)
			i = end
		case rs[i] == '`':
			end := indexRune(rs, '`', i+1)
			out = append(out, commands(string(rs[i+1:end]), depth+1)...)
			i = end
		}
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// indexRune returns the index of the first r at or after from, or len(rs).
func indexRune(rs []rune, r rune, from int) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == r {
			return i
		}
	}
	return len(rs)
}

// matchParen returns the index of the parenthesis that closes the one at open,
// or len(rs).
func matchParen(rs []rune, open int) int {
	depth := 0
	for i := open; i < len(rs); i++ {
		switch rs[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(rs)
}

// unwrap drops leading assignments and looks through wrappers, shells and
// eval, returning the commands they run, the wrapper command included.
func unwrap(c command, depth int) []command {
	for len(c.words) > 0 && isAssignment(c.words[0]) {
		c.words = c.words[1:]
	}
	if len(c.words) == 0 {
		return []command{c}
	}
	out := []command{c}
	prog := path.Base(c.words[0])
	args := c.words[1:]
	switch {
	case prog == "eval":
		out = append(out, commands(strings.Join(args, " "), depth+1)...)
	case shells[prog]:
		for i, a := range args {
			if strings.HasPrefix(a, "-") && strings.Contains(a, "c") && !strings.HasPrefix(a, "--") && i+1 < len(args) {
				out = append(out, commands(args[i+1], depth+1)...)
				break
			}
		}
	case wrappers[prog]:
		if rest := skipWrapperOptions(prog, args); len(rest) > 0 {
			out = append(out, unwrap(command{words: rest}, depth+1)...)
		}
		// Whatever the options looked like, a git or gh later in the line is judged.
		for j, a := range args {
			if hosting[path.Base(a)] || path.Base(a) == "git" {
				out = append(out, command{words: args[j:]})
				break
			}
		}
	}
	return out
}

func isAssignment(w string) bool {
	i := strings.Index(w, "=")
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// optionsWithValue lists, per wrapper, the options whose next word is their value.
var optionsWithValue = map[string][]string{
	"sudo": {"-u", "-g", "-h", "-p", "-r", "-t", "-C", "-D", "-U"}, "doas": {"-u", "-C"},
	"env": {"-u", "-C", "-S"}, "timeout": {"-s", "-k"}, "nice": {"-n"}, "ionice": {"-c", "-n", "-p"},
	"xargs": {"-n", "-I", "-L", "-P", "-s", "-d", "-E"}, "stdbuf": {"-i", "-o", "-e"}, "watch": {"-n", "-d"},
}

// skipWrapperOptions removes the options and the leading arguments of a
// wrapper (assignments of env, the duration of timeout) and returns the
// command that is left.
func skipWrapperOptions(prog string, args []string) []string {
	i := 0
	for i < len(args) && (strings.HasPrefix(args[i], "-") || isAssignment(args[i])) {
		if slices.Contains(optionsWithValue[prog], args[i]) {
			i++ // the value of the option
		}
		i++
	}
	if (prog == "timeout" || prog == "nice" || prog == "watch") && i < len(args) && !strings.HasPrefix(args[i], "-") && startsWithDigit(args[i]) {
		i++ // the duration, or the niceness
	}
	return args[i:]
}

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }
