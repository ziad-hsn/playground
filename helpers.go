package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

type WikiGrepOutput struct {
	Matches []string
	Title   string
	PageID  int
	Url     string
	Err     error
}

type WikiContent struct {
	Query struct {
		Pages map[string]struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Extract string `json:"extract"`
			FullUrl string `json:"fullurl"`
		} `json:"pages"`
	} `json:"query"`
}

type WikiTitleIDs struct {
	Query struct {
		SearchInfo struct {
			TotalHits  int    `json:"totalhits"`
			Suggestion string `json:"suggestion"`
		} `json:"searchinfo"`
		Search []map[string]interface{} `json:"search"`
	} `json:"query"`
}

type WikiPagesResponse struct {
	Pages      []int
	TotalHits  int
	Suggestion string
	Err        error
}

type SummaryResponse struct {
	Summary string
	Title   string
	PageID  int
	Url     string
	Err     error
}
type WikiSummaryResponse struct {
	Summary map[int]SummaryResponse
	Err     error
}

type BodyResponse struct {
	Body   string
	Title  string
	PageID int
	Url    string
	Err    error
}
type WikiBodyResponse struct {
	Body []BodyResponse
	Err  error
}

// GetJSON takes a context, URL, client, and target interface, sends an HTTP request
// to fetch data from an API endpoint in JSON format. The response is parsed into the
// provided target structure.
func GetJSON(ctx context.Context, reqURL string, client *http.Client, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)

	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	err = json.NewDecoder(resp.Body).Decode(target)
	if err != nil {

		return err
	}
	return nil
}

// GetWikiPagesAPI fetches Wikipedia pages based on a search term.
// It sends the results to the given output channel.
// Handles pagination with offset and limit.
// Meant for use with goroutines and a wait group.

func GetWikiPagesAPI(ctx context.Context, offset, limit int, search, word string, client *http.Client, out chan<- WikiPagesResponse) {

	defer close(out)
	var results WikiTitleIDs
	params := url.Values{}
	switch search {
	case "title":
		params.Add("srsearch", fmt.Sprintf("intitle:\"%s\"", word))
	default:
		params.Add("srsearch", fmt.Sprintf("\"%s\"", word))

	}
	reqURL := WikiAPIQueries("pages", limit, offset, &params)
	err := GetJSON(ctx, reqURL, client, &results)
	if err != nil || results.Query.Search == nil {
		out <- WikiPagesResponse{
			Err: err,
		}
		return
	}

	response := WikiPagesResponse{
		Err:        nil,
		TotalHits:  results.Query.SearchInfo.TotalHits,
		Pages:      make([]int, 0),
		Suggestion: results.Query.SearchInfo.Suggestion,
	}

	if len(results.Query.Search) > 0 {
		for _, s := range results.Query.Search {
			response.Pages = append(response.Pages, int(s["pageid"].(float64)))
		}
	}
	out <- response
	return
}

// GetWikiSummariesAPI fetches summary info for a list of Wikipedia page IDs.
// Sends the results to the provided output channel. Meant to be run in a goroutine with a wait group.
func GetWikiSummariesAPI(ctx context.Context, pages []int, client *http.Client, out chan<- WikiSummaryResponse, wg *sync.WaitGroup) {
	defer wg.Done()

	var results WikiContent
	response := WikiSummaryResponse{
		Summary: make(map[int]SummaryResponse),
	}
	params := url.Values{}
	searchPages := ""
	for _, p := range pages {
		searchPages += fmt.Sprintf("%d|", p)
	}
	params.Add("pageids", searchPages[:len(searchPages)-1])

	reqURL := WikiAPIQueries("summary", 0, 0, &params)
	err := GetJSON(ctx, reqURL, client, &results)

	fmt.Println(reqURL)
	if err != nil || results.Query.Pages == nil {
		response.Err = err
		select {
		case out <- response:
		case <-ctx.Done():
		}
		return
	}
	for _, page := range pages {
		pageIDStr := strconv.Itoa(page)
		currentSummaryData, ok := response.Summary[page]
		if !ok {
			currentSummaryData = SummaryResponse{PageID: page}
		}
		if _, ok := results.Query.Pages[pageIDStr]; !ok {
			currentSummaryData.Err = errors.New(fmt.Sprintf("no results for page %d: %v\n", pages, results.Query.Pages))
			response.Summary[page] = currentSummaryData
			continue

		}
		currentSummaryData.Url = results.Query.Pages[pageIDStr].FullUrl
		currentSummaryData.Title = results.Query.Pages[pageIDStr].Title

		currentSummaryData.Summary = results.Query.Pages[pageIDStr].Extract

		response.Summary[page] = currentSummaryData
	}

	response.Err = nil
	select {
	case out <- response:
	case <-ctx.Done():
	}
	return
}

// GetWikiBody fetches the wikitext (body) for a list of Wikipedia page IDs.
// Sends the results to the given output channel. Designed to work with goroutines and a wait group.
func GetWikiBody(ctx context.Context, pages []int, client *http.Client, out chan<- WikiBodyResponse, wg *sync.WaitGroup) {
	defer wg.Done()
	var results WikiContent
	response := WikiBodyResponse{
		Body: make([]BodyResponse, 0),
	}
	params := url.Values{}

	searchPages := ""
	for _, p := range pages {
		searchPages += fmt.Sprintf("%d|", p)
	}
	params.Add("pageids", searchPages[:len(searchPages)-1])

	reqURL := WikiAPIQueries("body", 0, 0, &params)
	//var x json.RawMessage

	err := GetJSON(ctx, reqURL, client, &results)

	if err != nil || results.Query.Pages == nil {
		response.Err = err
		select {
		case out <- response:
		case <-ctx.Done():
		}
		return
	}

	for _, data := range results.Query.Pages {
		// 'pages' is your int function argument
		currentBodyData := BodyResponse{}
		currentBodyData.PageID = data.PageID

		currentBodyData.Title = data.Title

		currentBodyData.Body = data.Extract
		currentBodyData.Url = data.FullUrl
		response.Body = append(response.Body, currentBodyData)
	}
	response.Err = nil
	select {
	case out <- response:
	case <-ctx.Done():
	}
	return
}

// WikiAPIQueries holds the API query templates or details for Wikipedia requests.
func WikiAPIQueries(action string, limit, offset int, params *url.Values) string {
	reqURL, _ := url.Parse("https://en.wikipedia.org/w/api.php")
	params.Add("action", "query")
	params.Add("format", "json")
	switch action {
	case "pages":

		params.Add("list", "search")
		if offset > 0 {
			params.Add("sroffset", strconv.Itoa(offset))
		}
		if limit < 0 {
			params.Add("srlimit", "50")
		} else {
			params.Add("srlimit", strconv.Itoa(limit))
		}
		params.Add("srsort", "incoming_links_desc")

	case "summary":
		params.Add("prop", "extracts|info")
		params.Add("exintro", "true")
		params.Add("explaintext", "true")
		params.Add("inprop", "url")
	case "body":
		params.Add("prop", "extracts|info")
		params.Add("explaintext", "true")
		params.Add("exlimit", "1")
		params.Add("inprop", "url")

	}

	reqURL.RawQuery = params.Encode()
	return reqURL.String()
}
