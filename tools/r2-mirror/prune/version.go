package prune

import "strings"

type byVersionSort []string

func (s byVersionSort) Len() int           { return len(s) }
func (s byVersionSort) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byVersionSort) Less(i, j int) bool { return VersionLess(s[i], s[j]) }

func VersionLess(a, b string) bool { return filevercmp(a, b) < 0 }

func filevercmp(a, b string) int {
	if c := verrevcmp(a[:filePrefixLen(a)], b[:filePrefixLen(b)]); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func filePrefixLen(s string) int {
	n := len(s)
	prefixLen := 0
	for i := 0; ; {
		if i == n {
			return prefixLen
		}
		i++
		prefixLen = i
		for i+1 < n && s[i] == '.' && (isASCIILetter(s[i+1]) || s[i+1] == '~') {
			for i += 2; i < n && (isASCIIAlnum(s[i]) || s[i] == '~'); i++ {
			}
		}
	}
}

func order(s string, pos int) int {
	if pos == len(s) {
		return -1
	}
	c := s[pos]
	switch {
	case isASCIIDigit(c):
		return 0
	case isASCIILetter(c):
		return int(c)
	case c == '~':
		return -2
	default:
		return int(c) + 256
	}
}

func isASCIIDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isASCIILetter(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
func isASCIIAlnum(c byte) bool  { return isASCIIDigit(c) || isASCIILetter(c) }

func verrevcmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		firstDiff := 0
		for (i < len(a) && !isASCIIDigit(a[i])) || (j < len(b) && !isASCIIDigit(b[j])) {
			ac := order(a, i)
			bc := order(b, j)
			if ac != bc {
				return sign(ac - bc)
			}
			i++
			j++
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		for i < len(a) && j < len(b) && isASCIIDigit(a[i]) && isASCIIDigit(b[j]) {
			if firstDiff == 0 {
				firstDiff = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isASCIIDigit(a[i]) {
			return 1
		}
		if j < len(b) && isASCIIDigit(b[j]) {
			return -1
		}
		if firstDiff != 0 {
			return sign(firstDiff)
		}
	}
	return 0
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
