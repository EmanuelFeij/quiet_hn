package main

import (
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EmanuelFeij/quiet_hn/hn"
)

func main() {
	// parse flags
	var port, cacheExpiration, numStories int
	flag.IntVar(&port, "port", 3000, "the port to start the web server on")
	flag.IntVar(&numStories, "num_stories", 30, "the number of top stories to display")
	flag.IntVar(&cacheExpiration, "cache_timer", 5, "the time that the cache takes to refresh")
	flag.Parse()

	cch := newCache(cacheExpiration + 2)

	tick := time.NewTicker(time.Duration(cacheExpiration) * time.Second)
	defer tick.Stop()

	go func() {
		for {
			select {
			case t := <-tick.C:

				stories := refillCache(numStories + 20)
				cch.m.Lock()
				cch.stories = stories
				cch.refreshDeadline()
				cch.m.Unlock()
				fmt.Println("Current time: ", t)
			}
		}

	}()

	tpl := template.Must(template.ParseFiles("./index.gohtml"))

	http.HandleFunc("/", handler(numStories, tpl, cch))

	// Start the server
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func handler(numStories int, tpl *template.Template, c *cache) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var stories []item

		stories = c.getFromCache(numStories)

		data := templateData{
			Stories: stories,
			Time:    time.Now().Sub(start),
		}
		err := tpl.Execute(w, data)
		if err != nil {
			http.Error(w, "Failed to process the template", http.StatusInternalServerError)
			return
		}
	})
}

func getItems(ids []int) []item {
	var client hn.Client
	stories := make([]item, 0)

	type result struct {
		idx   int
		story item
		err   error
	}

	ch := make(chan result)

	for i := 0; i < len(ids); i++ {

		go func(idx, id int) {
			hnItem, err := client.GetItem(id)
			if err != nil {
				ch <- result{idx: idx, err: errors.New("something wrong getting item")}
				return
			}
			item := parseHNItem(hnItem)
			if isStoryLink(item) {
				ch <- result{idx: idx, story: item, err: nil}
				return
			}
			ch <- result{idx: idx, err: errors.New("item its not a story")}
		}(i, ids[i])

	}

	var results []result
	for i := 0; i < len(ids); i++ {
		it := <-ch
		if it.err == nil {
			results = append(results, it)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].idx < results[j].idx
	})

	for _, it := range results {
		stories = append(stories, it.story)
	}

	return stories[:30]
}

type cache struct {
	expiration int
	deadline   time.Time
	stories    []item
	m          sync.Mutex
}

func newCache(expirationTime int) *cache {
	c := &cache{
		expiration: expirationTime,
		deadline:   time.Now(),
	}
	return c
}

func refillCache(numStories int) []item {

	var client hn.Client

	ids, err := client.TopItems()
	if err != nil {
		return nil
	}

	return getItems(ids[:numStories+20])

}

func (c *cache) refreshDeadline() {

	c.deadline = time.Now().Add(time.Duration(c.expiration) * time.Second)
}

func (c *cache) IsActive() bool {
	return c.deadline.Sub(time.Now()) > 0
}
func (c *cache) getFromCache(numStories int) []item {
	c.m.Lock()
	defer c.m.Unlock()
	if c.IsActive() {
		return c.stories
	}
	var client hn.Client

	ids, err := client.TopItems()
	if err != nil {
		return nil
	}

	c.stories = getItems(ids[:numStories+20])
	c.refreshDeadline()
	return c.stories
}

func isStoryLink(item item) bool {
	return item.Type == "story" && item.URL != ""
}

func parseHNItem(hnItem hn.Item) item {
	ret := item{Item: hnItem}
	url, err := url.Parse(ret.URL)
	if err == nil {
		ret.Host = strings.TrimPrefix(url.Hostname(), "www.")
	}
	return ret
}

// item is the same as the hn.Item, but adds the Host field
type item struct {
	hn.Item
	Host string
}

type templateData struct {
	Stories []item
	Time    time.Duration
}
