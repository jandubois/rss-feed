package main

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"gopkg.in/yaml.v3"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

// Configuration types

type Config struct {
	Sites map[string]SiteConfig `yaml:"sites"`
}

type SiteConfig struct {
	URL     string        `yaml:"url"`
	Index   IndexConfig   `yaml:"index"`
	Content ContentConfig `yaml:"content"`
	Feed    FeedConfig    `yaml:"feed"`
}

type IndexConfig struct {
	Item       string `yaml:"item"`
	Link       string `yaml:"link"`
	Date       string `yaml:"date"`
	DateAttr   string `yaml:"date_attr"`
	DateFormat string `yaml:"date_format"`
}

type ContentConfig struct {
	Container string            `yaml:"container"`
	Remove    []string          `yaml:"remove"`
	Styles    map[string]string `yaml:"styles"`
}

type FeedConfig struct {
	Title    string `yaml:"title"`
	Subtitle string `yaml:"subtitle"`
}

// Post holds metadata and content for a single blog post.
type Post struct {
	URL     string
	Title   string
	Date    *time.Time
	Content string
}

// CacheEntry is the JSON-serialized form of a cached post.
type CacheEntry struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Date    string `json:"date,omitempty"`
	Content string `json:"content"`
}

// Atom feed types

type AtomFeed struct {
	XMLName  xml.Name    `xml:"feed"`
	XMLNS    string      `xml:"xmlns,attr"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle,omitempty"`
	ID       string      `xml:"id"`
	Link     AtomLink    `xml:"link"`
	Updated  string      `xml:"updated"`
	Entries  []AtomEntry `xml:"entry"`
}

type AtomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type AtomEntry struct {
	Title     string      `xml:"title"`
	ID        string      `xml:"id"`
	Link      AtomLink    `xml:"link"`
	Published string      `xml:"published,omitempty"`
	Updated   string      `xml:"updated,omitempty"`
	Content   AtomContent `xml:"content"`
}

type AtomContent struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolving home directory: %v", err)
	}
	defaultConfig := filepath.Join(home, ".config", "rss-feed", "config.yaml")
	configPath := flag.String("config", defaultConfig, "path to config file")
	site := flag.String("site", "", "process only this site")
	limit := flag.Int("limit", 0, "max posts to process (0 = all)")
	delay := flag.Duration("delay", time.Second, "delay between HTTP fetches")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("rss-feed version " + version)
		return
	}

	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	for name, siteConfig := range config.Sites {
		if *site != "" && *site != name {
			continue
		}
		if err := processSite(name, siteConfig, *limit, *delay); err != nil {
			log.Printf("error processing %s: %v", name, err)
		}
	}
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func processSite(name string, config SiteConfig, limit int, delay time.Duration) error {
	home, _ := os.UserHomeDir()
	u, err := url.Parse(config.URL)
	if err != nil {
		return fmt.Errorf("parsing site URL: %w", err)
	}
	hostname := u.Hostname()
	cacheDir := filepath.Join(home, ".cache", "rss-feed", hostname)
	outputPath := filepath.Join(home, ".local", "share", "rss-feed", hostname+".xml")

	fmt.Printf("Fetching index: %s\n", config.URL)
	posts, err := fetchIndex(config)
	if err != nil {
		return fmt.Errorf("fetching index: %w", err)
	}
	fmt.Printf("Found %d posts\n", len(posts))

	if limit > 0 && len(posts) > limit {
		posts = posts[:limit]
		fmt.Printf("Processing %d posts\n", limit)
	}

	var entries []Post
	fetched := 0
	for i, post := range posts {
		cp := cachePath(cacheDir, post.URL)
		if cached, err := readCache(cp); err == nil {
			fmt.Printf("  [%d/%d] cached: %s\n", i+1, len(posts), post.Title)
			entries = append(entries, Post{
				URL:     post.URL,
				Title:   post.Title,
				Date:    post.Date,
				Content: cached.Content,
			})
			continue
		}

		if fetched > 0 {
			time.Sleep(delay)
		}
		fmt.Printf("  [%d/%d] fetching: %s\n", i+1, len(posts), post.Title)
		content, err := fetchContent(post.URL, config)
		if err != nil {
			log.Printf("  error fetching %s: %v", post.URL, err)
			continue
		}

		dateStr := ""
		if post.Date != nil {
			dateStr = post.Date.Format(time.RFC3339)
		}
		if err := writeCache(cp, CacheEntry{
			URL:     post.URL,
			Title:   post.Title,
			Date:    dateStr,
			Content: content,
		}); err != nil {
			log.Printf("  error caching %s: %v", post.URL, err)
		}

		entries = append(entries, Post{
			URL:     post.URL,
			Title:   post.Title,
			Date:    post.Date,
			Content: content,
		})
		fetched++
	}

	if err := generateFeed(config, entries, outputPath); err != nil {
		return fmt.Errorf("generating feed: %w", err)
	}
	fmt.Printf("Wrote %s (%d entries)\n", outputPath, len(entries))
	return nil
}

func fetchIndex(config SiteConfig) ([]Post, error) {
	resp, err := http.Get(config.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	baseURL, _ := url.Parse(config.URL)

	var posts []Post
	doc.Find(config.Index.Item).Each(func(_ int, item *goquery.Selection) {
		link := item.Find(config.Index.Link).First()
		if link.Length() == 0 {
			return
		}
		href, exists := link.Attr("href")
		if !exists {
			return
		}
		ref, _ := url.Parse(href)
		fullURL := baseURL.ResolveReference(ref).String()
		title := strings.TrimSpace(link.Text())

		var date *time.Time
		if config.Index.Date != "" {
			dateElem := item.Find(config.Index.Date).First()
			if dateElem.Length() > 0 {
				dateStr := strings.TrimSpace(dateElem.Text())
				if config.Index.DateAttr != "" {
					if v, exists := dateElem.Attr(config.Index.DateAttr); exists {
						dateStr = v
					}
				}
				if t, err := time.Parse(config.Index.DateFormat, dateStr); err == nil {
					t = t.UTC()
					date = &t
				}
			}
		}

		posts = append(posts, Post{URL: fullURL, Title: title, Date: date})
	})

	return posts, nil
}

func fetchContent(postURL string, config SiteConfig) (string, error) {
	resp, err := http.Get(postURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	container := doc.Find(config.Content.Container).First()
	if container.Length() == 0 {
		return "", fmt.Errorf("container %q not found", config.Content.Container)
	}

	for _, sel := range config.Content.Remove {
		container.Find(sel).Remove()
	}

	for sel, style := range config.Content.Styles {
		container.Find(sel).Each(func(_ int, s *goquery.Selection) {
			existing, _ := s.Attr("style")
			if existing != "" {
				style = existing + "; " + style
			}
			s.SetAttr("style", style)
		})
	}

	html, err := container.Html()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(html), nil
}

func generateFeed(config SiteConfig, entries []Post, outputPath string) error {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date == nil {
			return false
		}
		if entries[j].Date == nil {
			return true
		}
		return entries[i].Date.After(*entries[j].Date)
	})

	feed := AtomFeed{
		XMLNS:    "http://www.w3.org/2005/Atom",
		Title:    config.Feed.Title,
		Subtitle: config.Feed.Subtitle,
		ID:       config.URL,
		Link:     AtomLink{Href: config.URL, Rel: "alternate"},
		Updated:  time.Now().UTC().Format(time.RFC3339),
	}

	for _, entry := range entries {
		ae := AtomEntry{
			Title: entry.Title,
			ID:    entry.URL,
			Link:  AtomLink{Href: entry.URL, Rel: "alternate"},
			Content: AtomContent{
				Type:  "html",
				Value: entry.Content,
			},
		}
		if entry.Date != nil {
			ae.Published = entry.Date.Format(time.RFC3339)
			ae.Updated = entry.Date.Format(time.RFC3339)
		}
		feed.Entries = append(feed.Entries, ae)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	data, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, append([]byte(xml.Header), data...), 0644)
}

// Cache helpers

func cachePath(cacheDir, postURL string) string {
	u, err := url.Parse(postURL)
	if err != nil {
		h := sha256.Sum256([]byte(postURL))
		return filepath.Join(cacheDir, fmt.Sprintf("%x.json", h[:8]))
	}
	slug := path.Base(strings.TrimRight(u.Path, "/"))
	if slug == "" || slug == "." {
		h := sha256.Sum256([]byte(postURL))
		slug = fmt.Sprintf("%x", h[:8])
	}
	return filepath.Join(cacheDir, slug+".json")
}

func readCache(path string) (*CacheEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func writeCache(path string, entry CacheEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
