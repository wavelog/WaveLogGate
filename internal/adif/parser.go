package adif

import (
	"fmt"
	"regexp"
	"strings"
)

// fieldRe matches ADIF fields: <NAME:LEN> or <NAME:LEN:TYPE>
var fieldRe = regexp.MustCompile(`(?i)<([^:>]+):(\d+)(?::[^>]*)?>`)

// Parse parses an ADIF string and returns a map of UPPERCASE field names to values.
func Parse(adif string) map[string]string {
	result := make(map[string]string)
	matches := fieldRe.FindAllStringSubmatchIndex(adif, -1)
	for _, m := range matches {
		// m[2],m[3] = field name; m[4],m[5] = length
		name := strings.ToUpper(adif[m[2]:m[3]])
		lenStr := adif[m[4]:m[5]]
		var length int
		fmt.Sscanf(lenStr, "%d", &length)
		// Value starts immediately after the closing >
		start := m[1]
		end := start + length
		if end > len(adif) {
			end = len(adif)
		}
		if name != "EOR" && name != "EOH" {
			result[name] = adif[start:end]
		}
	}
	return result
}

// MapToADIF serializes a map of fields back to ADIF string format.
func MapToADIF(fields map[string]string) string {
	var sb strings.Builder
	for k, v := range fields {
		sb.WriteString(fmt.Sprintf("<%s:%d>%s ", strings.ToUpper(k), len(v), v))
	}
	sb.WriteString("<EOR>")
	return sb.String()
}
