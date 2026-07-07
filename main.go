package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
)

func main() {
	startURL := "https://regulation.tax.gov.ir/lwse?act=all&fr=0&sr=desk&s281009734=&s281010109%5B%5D=s281010108&s281010109%5B%5D=s281010107&textin=s281010103&all_s281010055=1&s281010055%5B%5D=s280965215&s281010055%5B%5D=s280965218&s281010055%5B%5D=s280971619&s281010055%5B%5D=s281010065&s281010038=&s281010039=&s281010046=&s281010051=&s281010099=&bps5000021601=#"

	outputFile := "output.md"
	f, err := os.OpenFile(outputFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatalf("Failed to open output file: %v", err)
	}
	defer f.Close()

	var mu sync.Mutex

	c := colly.NewCollector(
		colly.AllowedDomains("regulation.tax.gov.ir"),
		colly.Async(true),
	)
	
	ruleCollector := c.Clone()

	c.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: 1,
		Delay:       1 * time.Second,
	})
	ruleCollector.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: 2,
		Delay:       1 * time.Second,
	})

	pageCount := 1
	var visitedPages sync.Map

	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting list page:", r.URL.String())
	})

	// 1. List all <a> tags with href that contains "lwvi?id=" or "lwvi?lid="
	c.OnHTML("a[href*='lwvi?id='], a[href*='lwvi?lid=']", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		ruleURL := e.Request.AbsoluteURL(link)
		// 2. Open the links of each rule
		ruleCollector.Visit(ruleURL)
	})

	// 6. Use the pagination nav and go to next page
	c.OnHTML("body > div:nth-child(2) > main > div:nth-child(2) > form > div:nth-child(3) > div:nth-child(1) > div:nth-child(22) > nav, nav", func(e *colly.HTMLElement) {
		e.ForEachWithBreak("a[href]", func(_ int, el *colly.HTMLElement) bool {
			text := strings.TrimSpace(el.Text)
			href := el.Attr("href")
			
			if strings.Contains(text, "بعدی") || text == fmt.Sprintf("%d", pageCount+1) {
				nextPageURL := ""
				if strings.HasPrefix(href, "javascript:") {
					if strings.Contains(startURL, "?") {
						nextPageURL = fmt.Sprintf("%s&page=%d", startURL, pageCount+1)
					} else {
						nextPageURL = fmt.Sprintf("%s?page=%d", startURL, pageCount+1)
					}
				} else if href != "#" {
					nextPageURL = e.Request.AbsoluteURL(href)
				}
				
				if nextPageURL != "" {
					if _, loaded := visitedPages.LoadOrStore(nextPageURL, true); !loaded {
						pageCount++
						fmt.Println("Found next page:", nextPageURL)
						// 7. go to next page to do the same until there is no page left
						c.Visit(nextPageURL)
						return false // break out of ForEachWithBreak
					}
				}
			}
			return true // continue
		})
	})

	ruleCollector.OnRequest(func(r *colly.Request) {
		fmt.Println("  Visiting rule page:", r.URL.String())
	})

	reChlist := regexp.MustCompile(`(?s)let\s+chlist\s*=\s*(\[.*?\])\s*;`)

	ruleCollector.OnHTML("html", func(e *colly.HTMLElement) {
		mainHeaderXPath := "body > div:nth-child(2) > main > div > div:nth-child(1) > div:nth-child(2) > div:nth-child(1) > div"
		mainHeader := strings.TrimSpace(e.DOM.Find(mainHeaderXPath).Text())
		
		if mainHeader == "" {
			mainHeader = strings.TrimSpace(e.DOM.Find("title").Text())
		}

		var sb strings.Builder
		
		sb.WriteString(fmt.Sprintf("# %s\n\n", mainHeader))
		sb.WriteString(fmt.Sprintf("**Source:** [%s](%s)\n\n", e.Request.URL.String(), e.Request.URL.String()))

		// Colly cannot execute the JS that populates div.row-itm. 
		// Instead, we extract the JSON array "chlist" embedded in the page's script.
		rawHtml := string(e.Response.Body)
		matches := reChlist.FindStringSubmatch(rawHtml)
		
		if len(matches) > 1 {
			jsonStr := matches[1]
			var data []map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err == nil {
				for _, item := range data {
					sectionHeader := ""
					pty, _ := item["pty"].(string)
					if pty == "s280984915" || strings.Contains(e.Request.URL.String(), "ty=qh") {
						sectionHeader, _ = item["ti"].(string)
					} else {
						sectionHeader, _ = item["pty_ti"].(string)
						if sectionHeader == "" {
							sectionHeader, _ = item["ti"].(string)
						}
					}
					
					sectionText := ""
					if val, ok := item["s80512493"].(string); ok && val != "" {
						sectionText = val
					}

					// Clean HTML tags from the embedded text
					doc, _ := goquery.NewDocumentFromReader(strings.NewReader(sectionText))
					cleanText := strings.TrimSpace(doc.Text())
					cleanText = strings.ReplaceAll(cleanText, "\u200c", " ")

					if sectionHeader != "" {
						sb.WriteString(fmt.Sprintf("## %s\n\n", sectionHeader))
					}
					if cleanText != "" {
						sb.WriteString(fmt.Sprintf("%s\n\n", cleanText))
					}
				}
			}
		}

		sb.WriteString("---\n\n")

		mu.Lock()
		f.WriteString(sb.String())
		mu.Unlock()
	})

	visitedPages.Store(startURL, true)
	err = c.Visit(startURL)
	if err != nil {
		log.Fatalf("Failed to start crawler: %v", err)
	}
	
	c.Wait()
	ruleCollector.Wait()
	fmt.Println("Scraping completed.")
}
