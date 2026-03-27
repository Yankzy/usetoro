package main

import (
	"fmt"
	"regexp"
	"strings"
)

func main() {
	raw := "MCDONALD'S F12349 STR# 992"
	fixHashRegex := regexp.MustCompile(`([^#\s]+)#\s*([^#\s]+)`)
	
	cleaned := strings.ReplaceAll(raw, "!", "")
	res := fixHashRegex.ReplaceAllString(cleaned, "$1 #$2")
	fmt.Printf("Regex Result: %q\n", res)
}
