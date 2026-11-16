package library

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxFilenamePartBytes = 80
)

func ExportName(name, version string, appId int64) string {
	safe := func(value string) string {
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || strings.ContainsRune(`/\<>:"|?*`, r) {
				return '_'
			}
			return r
		}, value)
		value = strings.Trim(value, " .")
		for len(value) > maxFilenamePartBytes {
			_, size := utf8.DecodeLastRuneInString(value)
			value = value[:len(value)-size]
		}
		return value
	}

	if appId < 0 {
		return fmt.Sprintf("%s_%s.ipa", safe(name), safe(version))
	}
	return fmt.Sprintf("%s_%s_%d.ipa", safe(name), safe(version), appId)
}
