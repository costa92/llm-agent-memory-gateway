package service

import "unicode/utf8"

func EstimateTokenCost(content string) int {
	if content == "" {
		return 0
	}

	runeCount := utf8.RuneCountInString(content)
	return (runeCount + 3) / 4
}
