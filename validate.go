package main

import (
	"net/url"
	"regexp"
	"strings"
)

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,20}$`)

func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)

	if raw == "" {
		return raw
	}

	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}

	return raw
}

func validURL(raw string) bool {
	if raw == "" || len(raw) > 2048 {
		return false
	}
	_, err := url.Parse(raw)
	return err == nil
}

func validAlias(alias string) bool {
	return aliasPattern.MatchString(alias)
}
