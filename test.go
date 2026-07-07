package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func main() {
	b, err := os.ReadFile("test_html.html")
	if err != nil {
		panic(err)
	}
	html := string(b)

	re := regexp.MustCompile(`(?s)let\s+chlist\s*=\s*(\[.*?\])\s*;`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		jsonStr := matches[1]
		var data []map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			panic(err)
		}
		
		for _, item := range data {
			header := ""
			if val, ok := item["ti"].(string); ok && val != "" {
				header = val
			} else if val, ok := item["pty_ti"].(string); ok && val != "" {
				header = val
			}
			
			text := ""
			if val, ok := item["s80512493"].(string); ok && val != "" {
				text = val
			}

			// Clean HTML from text
			doc, _ := goquery.NewDocumentFromReader(strings.NewReader(text))
			cleanText := doc.Text()

			fmt.Printf("Header: %s\nText: %s\n", header, cleanText)
		}
	} else {
		fmt.Println("No chlist found")
	}
}
