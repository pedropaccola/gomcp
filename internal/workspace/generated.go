package workspace

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// generatedPattern matches the standard marker go/generate documents and
// every codegen tool (deepcopy-gen, protoc, mockgen, and alike) emits
// verbatim as one whole leading comment line.
var generatedPattern = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// IsGenerated reports whether src carries a generated-file marker among
// its leading comment/blank lines, before the first line of real content
// — checked fresh against whatever bytes are given, never cached as
// workspace state.
func IsGenerated(src []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		if generatedPattern.MatchString(line) {
			return true
		}
		if !strings.HasPrefix(line, "//") {
			return false
		}
	}
	return false
}
