package main

import (
	"context"
	"flag"
	"log"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	word := flag.String("word", "Mr. Immortal", "search pattern")
	search := flag.String("search", "body", "title or body")
	mode := flag.String("mode", "grep", "summary or grep")
	limit := flag.Int("limit", 5, "max 50 pages")
	after := flag.Int("after", 50, "number of words to get after the phrase")
	before := flag.Int("before", 50, "number of words to get before the phrase")
	flag.Parse()

	if *limit > 50 {
		log.Fatal("maximum 50 pages per search")
	}
	switch *mode {
	case "summary":
		pipeline := getWikiSummary(ctx, Take(ctx, *limit, getWikiPages(ctx, *word, *search)))
		PrintWikiSummaries(pipeline)
	case "grep":
		pipeline := GrepWiki(ctx, *word, *after, *before, getWikiContent(ctx, WikiLimit(ctx, 1, Take(ctx, *limit, getWikiPages(ctx, *word, *search)))))
		PrintWikiGrepResults(pipeline, *word)

	}

}
