package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Ignore implements a small .einoignore matcher. Patterns are gitignore-like
// for the common cases: comments, blank lines, ! negation, trailing / for
// directories, leading / for root anchoring, * and ** globs.
type Ignore struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	re       *regexp.Regexp
	negated  bool
	dirOnly  bool
	anchored bool
	raw      string
}

// LoadIgnore reads <root>/.einoignore. A missing file is not an error.
func LoadIgnore(root string) *Ignore {
	f, err := os.Open(filepath.Join(root, ".einoignore"))
	if err != nil {
		return &Ignore{}
	}
	defer f.Close()
	var patterns []ignorePattern
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := ignorePattern{raw: line}
		if strings.HasPrefix(line, "!") {
			p.negated = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
			if line == "" {
				continue
			}
		}
		if strings.HasPrefix(line, "/") {
			p.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		p.re = globRegexp(line, p.anchored)
		patterns = append(patterns, p)
	}
	return &Ignore{patterns: patterns}
}

// Match reports whether a workspace-relative slash path is ignored.
func (i *Ignore) Match(rel string) bool {
	if i == nil {
		return false
	}
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" || rel == "." {
		return false
	}
	ignored := false
	for _, p := range i.patterns {
		if p.matches(rel) {
			ignored = !p.negated
		}
	}
	return ignored
}

func (p ignorePattern) matches(rel string) bool {
	parts := strings.Split(rel, "/")
	var candidates []string
	if p.anchored {
		for n := 1; n <= len(parts); n++ {
			candidates = append(candidates, strings.Join(parts[:n], "/"))
		}
	} else if p.dirOnly {
		for n := 1; n <= len(parts); n++ {
			candidates = append(candidates, strings.Join(parts[:n], "/"))
		}
	} else {
		candidates = append(candidates, rel)
		for _, part := range parts {
			if part != rel {
				candidates = append(candidates, part)
			}
		}
	}
	for _, c := range candidates {
		if p.re.MatchString(c) {
			return true
		}
	}
	return false
}

func globRegexp(pattern string, anchored bool) *regexp.Regexp {
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString("(^|/)")
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`^$a`)
	}
	return re
}
