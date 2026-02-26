# rss-feed

Generate Atom feeds from websites that lack them. Define each site's structure with CSS selectors in a YAML config file; rss-feed scrapes the index page, extracts post content, and produces a valid Atom feed.

Fetched articles are cached locally, so repeated runs only fetch new posts.

## Install

```sh
go install github.com/jandubois/rss-feed@latest
```

Or build from source:

```sh
go build -o rss-feed .
cp rss-feed /usr/local/bin/
```

## Usage

```
rss-feed                           # process all configured sites
rss-feed --site myblog             # process one site
rss-feed --limit 10                # only process the 10 newest posts
rss-feed --delay 2s                # wait 2 seconds between fetches
rss-feed --config /path/to/config  # use a different config file
rss-feed --version                 # print version
```

Cached articles are reused across runs. To fetch all posts from a new site without overwhelming the server, increase `--limit` gradually over several runs; each run fetches only the uncached posts.

## Configuration

Default config path: `~/.config/rss-feed/config.yaml`

```yaml
sites:
  myblog:
    url: "https://example.com/blog/"
    index:
      item: "ul li"              # selector for each post entry
      link: "a"                  # sub-selector for the post link
      date: "time"              # sub-selector for the date element
      date_attr: "datetime"      # attribute containing the date value
      date_format: "2006-01-02"  # Go time reference format
    content:
      container: "body"          # element containing the post body
      remove:                    # chrome elements to strip
        - "nav"
        - "footer"
        - "script"
        - "style"
    feed:
      title: "My Blog"
      subtitle: "Optional subtitle"
```

### Adding a site

Each key under `sites` is an arbitrary name used with `--site` to process a single site. To add a site:

1. Inspect the blog's index page and identify the HTML structure of post listings.
2. Set `index.item` to select each post entry (e.g., `article`, `ul li`, `.post-list > div`).
3. Set `index.link` to select the link element within each entry.
4. If dates appear in the HTML, set `index.date` to select the date element and `index.date_attr` to the attribute holding the date string. Omit both if the index has no dates.
5. Set `index.date_format` to a [Go time reference format](https://pkg.go.dev/time#pkg-constants) matching the date string.
6. Inspect an individual post page. Set `content.container` to the element wrapping the post body, and list elements to strip in `content.remove`.
7. Set `feed.title` and optionally `feed.subtitle`.

### File locations

| Purpose | Path |
|---------|------|
| Config  | `~/.config/rss-feed/config.yaml` |
| Cache   | `~/.cache/rss-feed/<hostname>/` |
| Output  | `~/.local/share/rss-feed/<hostname>.xml` |

The hostname is derived from each site's URL.

## License

Apache License 2.0. See [LICENSE](LICENSE).
