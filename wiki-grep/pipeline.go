package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"
)

//func URLFeeder(ctx context.Context, urls []string) <-chan string {
//	//ctx, cancel := context.WithCancel(ctx)
//	feeder := make(chan string, len(urls))
//	go func() {
//		defer close(feeder)
//		for _, url := range urls {
//			select {
//			case feeder <- url:
//			case <-ctx.Done():
//				return
//
//			}
//		}
//	}()
//	return feeder
//}
//
//func IntSliceChan(ctx context.Context, inputChan <-chan WikiPagesResponse) <-chan []int {
//	intChan := make(chan []int, cap(inputChan))
//	go func() {
//		defer close(intChan)
//		for input := range inputChan {
//			select {
//			case intChan <- input.Pages:
//			case <-ctx.Done():
//				return
//			}
//
//		}
//
//	}()
//	return intChan
//}
//
//func URLFetcher(ctx context.Context, workers int, feeder <-chan string) <-chan Response {
//	fetcher := make(chan Response, workers)
//	go func() {
//		sem := make(chan struct{}, workers)
//		ctx, cancel := context.WithCancel(ctx)
//		defer close(fetcher)
//		defer close(sem)
//		defer cancel()
//		client := http.Client{Timeout: 10 * time.Second}
//	loop:
//		for {
//			select {
//			case req, ok := <-feeder:
//				if !ok {
//					break loop
//				}
//				fmt.Println("got url from feeder", req)
//				go func() {
//					sem <- struct{}{}
//					select {
//					case <-ctx.Done():
//						return
//					default:
//
//					}
//					response := Response{url: req}
//					resp, err := client.Get(req)
//					if err != nil {
//						response.err = err
//						fetcher <- response
//						<-sem
//						return
//					}
//
//					body, err := io.ReadAll(resp.Body)
//
//					if err != nil && err != io.EOF {
//						response.err = err
//						fetcher <- response
//						<-sem
//						return
//
//					}
//					response.body = body
//					response.status = resp.Status
//					response.statusCode = resp.StatusCode
//
//					fetcher <- response
//					<-sem
//					return
//
//				}()
//			case <-ctx.Done():
//				return
//
//			}
//		}
//		<-ctx.Done()
//	}()
//	return fetcher
//}

// Take take n values only from upstream stage
func Take(ctx context.Context, count int, generator <-chan WikiPagesResponse) <-chan WikiPagesResponse {
	takeChan := make(chan WikiPagesResponse, 1)
	go func() {
		defer close(takeChan)
		select {
		case <-ctx.Done():
			return
		case take, ok := <-generator:
			if !ok {
				return
			}
			take.Pages = take.Pages[:min(len(take.Pages), count)]

			select {
			case takeChan <- take:
				return
			case <-ctx.Done():
				return

			}
		}

	}()
	return takeChan
}

// WikiLimit limits the upstream stage value to be processed in chunks

func WikiLimit(ctx context.Context, limit int, pageChunk <-chan WikiPagesResponse) <-chan []int {
	limitChan := make(chan []int, limit)

	go func() {
		defer close(limitChan)

		for {
			select {
			case <-ctx.Done():
				return
			case pages, ok := <-pageChunk:
				if !ok {
					return
				}
				if pages.TotalHits <= limit {
					limitChan <- pages.Pages
					return
				}
				for i := 0; i < min(len(pages.Pages), 50); i += limit {
					select {
					case limitChan <- pages.Pages[i : i+limit]:
					case <-ctx.Done():
						return

					}
				}
				return
			}
		}
	}()
	return limitChan
}

// GrepWiki filters page contents from the upstream stage, emitting grep results where the word is found.
func GrepWiki(ctx context.Context, word string, after int, before int, contents <-chan WikiBodyResponse) <-chan WikiGrepOutput {
	results := make(chan WikiGrepOutput, cap(contents))
	go func() {
		defer close(results)
		for {
			select {
			case content, ok := <-contents:
				if !ok {
					return
				}
				if content.Err != nil {
					fmt.Printf("an error in the returned content: %v\n", content.Err)
					continue
				}

				for _, page := range content.Body {

					r := WikiGrepOutput{
						PageID: page.PageID,
						Title:  page.Title,
						Url:    page.Url}
					if page.Err != nil {
						r.Err = page.Err
						select {
						case results <- r:
						case <-ctx.Done():
							return

						}
					}

					matches := make([]string, 0)
					pattern := fmt.Sprintf("(?i)%s", word)
					regex := regexp.MustCompile(pattern)
					finds := regex.FindAllStringIndex(page.Body, -1)

					for _, match := range finds {
						snippet := extractContextByWords(
							page.Body,
							[2]int{match[0], match[1]},
							before, // words before
							after,  // words after
						)
						matches = append(matches, snippet)
					}
					r.Matches = matches
					select {
					case results <- r:

					case <-ctx.Done():
						return
					}
				}
			case <-ctx.Done():
				return

			}
		}
	}()
	return results
}

// getWikiPages collects Wikipedia pages from the API as the first upstream stage in the pipeline.
func getWikiPages(ctx context.Context, word string, search string) <-chan WikiPagesResponse {
	pageIDs := make(chan WikiPagesResponse, 1)
	go func() {
		defer close(pageIDs)
		client := http.Client{
			Timeout: 2 * time.Second,
		}
		defer client.CloseIdleConnections()

		out := make(chan WikiPagesResponse, 1)
		// limit is hardcoded = 50
		go GetWikiPagesAPI(ctx, 0, 50, search, word, &client, out)
		for resp := range out {
			if resp.Err != nil {
				fmt.Println(resp.Err)
				return
			}
			if resp.TotalHits == 0 {
				return
			}
			select {
			case pageIDs <- resp:
			case <-ctx.Done():
				return

			}
		}
	}()
	return pageIDs
}

// getWikiSummary is a pipeline stage that transforms Wikipedia page responses from upstream into summary results.
func getWikiSummary(ctx context.Context, pagesResponse <-chan WikiPagesResponse) <-chan WikiSummaryResponse {
	summaries := make(chan WikiSummaryResponse, cap(pagesResponse))
	go func() {
		defer close(summaries)
		wg := &sync.WaitGroup{}
		client := http.Client{Timeout: 2 * time.Second}

		for pages := range pagesResponse {
			wg.Add(1)
			go GetWikiSummariesAPI(ctx, pages.Pages, &client, summaries, wg)
		}
		wg.Wait()
		return
	}()
	return summaries
}

func getWikiContent(ctx context.Context, pages <-chan []int) <-chan WikiBodyResponse {
	body := make(chan WikiBodyResponse, cap(pages))
	go func() {
		defer close(body)
		wg := &sync.WaitGroup{}
		client := http.Client{Timeout: 2 * time.Second}
		for page := range pages {
			wg.Add(1)
			go GetWikiBody(ctx, page, &client, body, wg)
		}
		wg.Wait()
		return
	}()
	return body
}
