package main

import (
	"encoding/csv"
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

type RuleState struct {
	Link   string
	Status string
}

const (
	StatusPending   = "PENDING"
	StatusExtracted = "EXTRACTED"
)

var (
	rulesMap  = make(map[string]*RuleState)
	rulesList []string // maintain order
	csvMutex  sync.Mutex
	csvFile   = "rules.csv"
	outFile   = "output.md"
)

func loadState() {
	f, err := os.Open(csvFile)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Fatalf("Failed to open %s: %v", csvFile, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		log.Fatalf("Failed to read %s: %v", csvFile, err)
	}

	for _, row := range records {
		if len(row) >= 2 {
			link, status := row[0], row[1]
			if _, exists := rulesMap[link]; !exists {
				rulesList = append(rulesList, link)
			}
			rulesMap[link] = &RuleState{Link: link, Status: status}
		}
	}
}

func saveState() {
	csvMutex.Lock()
	defer csvMutex.Unlock()

	f, err := os.OpenFile(csvFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("Failed to write state: %v", err)
		return
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	for _, link := range rulesList {
		rule := rulesMap[link]
		_ = writer.Write([]string{rule.Link, rule.Status})
	}
}

func appendState(link, status string) {
	csvMutex.Lock()
	defer csvMutex.Unlock()
	f, err := os.OpenFile(csvFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to append state: %v", err)
		return
	}
	defer f.Close()
	writer := csv.NewWriter(f)
	defer writer.Flush()
	_ = writer.Write([]string{link, status})
}

func main() {
	// Start URL for indexing
	startURL := "https://regulation.tax.gov.ir/lwse?act=all&fr=0&sr=desk&s281009734=&s281010109%5B%5D=s281010108&s281010109%5B%5D=s281010107&textin=s281010103&s281010038=&s281010039=&s281010046=&s281010051=&bps5000021601="

	fmt.Println("--- Loading previous state ---")
	loadState()
	fmt.Printf("Loaded %d rules from %s\n", len(rulesList), csvFile)

	fmt.Println("\n--- Phase 1: Indexing & Discovery ---")
	c := colly.NewCollector(
		colly.AllowedDomains("regulation.tax.gov.ir"),
		colly.Async(true),
	)
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: 1,
		Delay:       1 * time.Second,
	})

	pageCount := 1
	var visitedPages sync.Map

	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting list page:", r.URL.String())
	})

	c.OnHTML("a[href*='lwvi?id='], a[href*='lwvi?lid=']", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		ruleURL := e.Request.AbsoluteURL(link)

		csvMutex.Lock()
		if _, exists := rulesMap[ruleURL]; !exists {
			rulesMap[ruleURL] = &RuleState{Link: ruleURL, Status: StatusPending}
			rulesList = append(rulesList, ruleURL)
			csvMutex.Unlock()
			
			// Append immediately to ensure fault tolerance during Phase 1
			appendState(ruleURL, StatusPending)
			fmt.Println("  Found new rule:", ruleURL)
		} else {
			csvMutex.Unlock()
		}
	})

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
						c.Visit(nextPageURL)
						return false // break out of ForEachWithBreak
					}
				}
			}
			return true
		})
	})

	visitedPages.Store(startURL, true)
	err := c.Visit(startURL)
	if err != nil {
		log.Printf("Failed to start indexing: %v", err)
	}
	c.Wait()
	fmt.Println("Indexing complete.")

	fmt.Println("\n--- Phase 2: Extraction ---")
	
	// Create output file if it doesn't exist, otherwise append
	fOut, err := os.OpenFile(outFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open output file: %v", err)
	}
	defer fOut.Close()

	var mu sync.Mutex // For writing to markdown

	ruleCollector := colly.NewCollector(
		colly.AllowedDomains("regulation.tax.gov.ir"),
		colly.Async(true),
	)
	ruleCollector.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: 2,
		Delay:       1 * time.Second,
	})

	reChlist := regexp.MustCompile(`(?s)let\s+chlist\s*=\s*(\[.*?\])\s*;`)

	ruleCollector.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting rule page:", r.URL.String())
	})

	ruleCollector.OnHTML("html", func(e *colly.HTMLElement) {
		ruleURL := e.Request.URL.String()

		mainHeaderXPath := "body > div:nth-child(2) > main > div > div:nth-child(1) > div:nth-child(2) > div:nth-child(1) > div"
		mainHeader := strings.TrimSpace(e.DOM.Find(mainHeaderXPath).Text())
		
		if mainHeader == "" {
			mainHeader = strings.TrimSpace(e.DOM.Find("title").Text())
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n\n", mainHeader))
		sb.WriteString(fmt.Sprintf("**Source:** [%s](%s)\n\n", ruleURL, ruleURL))

		rawHtml := string(e.Response.Body)
		matches := reChlist.FindStringSubmatch(rawHtml)
		
		if len(matches) > 1 {
			jsonStr := matches[1]
			var data []map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &data); err == nil {
				for _, item := range data {
					sectionHeader := ""
					pty, _ := item["pty"].(string)
					if pty == "s280984915" || strings.Contains(ruleURL, "ty=qh") {
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
		} else {
			sb.WriteString("_No extractable rule text found in page source._\n\n")
		}

		sb.WriteString("---\n\n")

		mu.Lock()
		fOut.WriteString(sb.String())
		mu.Unlock()

		csvMutex.Lock()
		if rule, exists := rulesMap[ruleURL]; exists {
			rule.Status = StatusExtracted
		}
		csvMutex.Unlock()
		saveState()
	})

	for _, link := range rulesList {
		rule := rulesMap[link]
		if rule.Status == StatusPending {
			ruleCollector.Visit(link)
		}
	}

	ruleCollector.Wait()
	fmt.Println("\nAll extractions completed.")
}
