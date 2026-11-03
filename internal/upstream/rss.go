package upstream

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"regexp"
	"strings"
)

// RSS-/Atom-Parser mit der Ausgabeform des frueheren Node-Dienstes
// (rss-parser + buildOutput), damit rss_reader.js unveraendert bleibt:
//
//	{status:"ok", feed:{url,title,link,author,description,image},
//	 items:[{title,pubDate,link,guid,author,thumbnail,description,content,categories}]}

type anyElem struct {
	XMLName xml.Name
	Text    string `xml:",chardata"`
}

type rssEnclosure struct {
	URL string `xml:"url,attr"`
}

type rssItem struct {
	Title       string       `xml:"title"`
	Link        string       `xml:"link"`
	PubDate     string       `xml:"pubDate"`
	GUID        string       `xml:"guid"`
	Author      string       `xml:"author"`
	Description string       `xml:"description"`
	Creator     string       `xml:"http://purl.org/dc/elements/1.1/ creator"`
	DCDate      string       `xml:"http://purl.org/dc/elements/1.1/ date"`
	Content     string       `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Enclosure   rssEnclosure `xml:"enclosure"`
	Categories  []string     `xml:"category"`
	Other       []anyElem    `xml:",any"`
}

type rssImage struct {
	URL string `xml:"url"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Author      string    `xml:"managingEditor"`
	Image       rssImage  `xml:"image"`
	Items       []rssItem `xml:"item"`
}

type rssRoot struct {
	XMLName xml.Name
	Channel rssChannel `xml:"channel"`
	// RSS 1.0 (RDF): Items liegen neben dem channel
	Items []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Links      []atomLink     `xml:"link"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	ID         string         `xml:"id"`
	Author     atomPerson     `xml:"author"`
	Summary    string         `xml:"summary"`
	Content    string         `xml:"content"`
	Categories []atomCategory `xml:"category"`
}

type atomFeed struct {
	XMLName  xml.Name
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle"`
	Links    []atomLink  `xml:"link"`
	Author   atomPerson  `xml:"author"`
	Logo     string      `xml:"logo"`
	Entries  []atomEntry `xml:"entry"`
}

var reTags = regexp.MustCompile(`(?s)<[^>]*>`)
var reSpace = regexp.MustCompile(`\s+`)

// Snippet: HTML entfernen, Entities aufloesen, Whitespace glaetten (contentSnippet).
func Snippet(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ") // &nbsp; zaehlt in Go-Regexp nicht als \s
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

func newDecoder(raw []byte) *xml.Decoder {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(strings.TrimSpace(charset)) {
		case "iso-8859-1", "latin1", "latin-1", "iso-8859-15", "windows-1252", "cp1252":
			return &latin1Reader{r: input}, nil
		}
		return input, nil
	}
	return dec
}

// latin1Reader wandelt Einzelbyte-Zeichensaetze (Latin-1-Familie) nach
// UTF-8: jedes Byte ist eine Rune. Reicht fuer Nachrichten-Feeds ohne
// zusaetzliche Abhaengigkeit.
type latin1Reader struct {
	r   io.Reader
	buf []byte
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	if len(l.buf) == 0 {
		tmp := make([]byte, len(p)/2+1)
		n, err := l.r.Read(tmp)
		if n == 0 {
			return 0, err
		}
		out := make([]byte, 0, n*2)
		for _, b := range tmp[:n] {
			if b < 0x80 {
				out = append(out, b)
			} else {
				out = append(out, 0xC0|b>>6, 0x80|b&0x3F)
			}
		}
		l.buf = out
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}

func atomAlternate(links []atomLink) string {
	for _, l := range links {
		if l.Rel == "" || l.Rel == "alternate" {
			return strings.TrimSpace(l.Href)
		}
	}
	if len(links) > 0 {
		return strings.TrimSpace(links[0].Href)
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// ParseFeed liest RSS 2.0, RSS 1.0 (RDF) oder Atom.
func ParseFeed(raw []byte, feedURL string) (map[string]any, error) {
	// Wurzelelement bestimmen
	var root struct{ XMLName xml.Name }
	if err := newDecoder(raw).Decode(&root); err != nil {
		return nil, errors.New("feed: kein XML")
	}
	items := []map[string]any{}
	feed := map[string]any{"url": feedURL, "title": "", "link": "", "author": "", "description": "", "image": ""}
	switch strings.ToLower(root.XMLName.Local) {
	case "feed":
		var f atomFeed
		if err := newDecoder(raw).Decode(&f); err != nil {
			return nil, err
		}
		feed["title"] = strings.TrimSpace(f.Title)
		feed["link"] = atomAlternate(f.Links)
		feed["author"] = strings.TrimSpace(f.Author.Name)
		feed["description"] = strings.TrimSpace(f.Subtitle)
		feed["image"] = strings.TrimSpace(f.Logo)
		for _, e := range f.Entries {
			link := atomAlternate(e.Links)
			cats := []string{}
			for _, c := range e.Categories {
				if c.Term != "" {
					cats = append(cats, c.Term)
				}
			}
			content := firstNonEmpty(e.Content, e.Summary)
			items = append(items, map[string]any{
				"title":       Snippet(e.Title),
				"pubDate":     firstNonEmpty(e.Published, e.Updated),
				"link":        link,
				"guid":        firstNonEmpty(e.ID, link),
				"author":      strings.TrimSpace(e.Author.Name),
				"thumbnail":   "",
				"description": Snippet(firstNonEmpty(e.Summary, e.Content)),
				"content":     content,
				"categories":  cats,
			})
		}
	default: // rss, RDF
		var r rssRoot
		if err := newDecoder(raw).Decode(&r); err != nil {
			return nil, err
		}
		ch := r.Channel
		feed["title"] = strings.TrimSpace(ch.Title)
		feed["link"] = strings.TrimSpace(ch.Link)
		feed["author"] = strings.TrimSpace(ch.Author)
		feed["description"] = Snippet(ch.Description)
		feed["image"] = strings.TrimSpace(ch.Image.URL)
		list := ch.Items
		if len(list) == 0 {
			list = r.Items
		}
		for _, it := range list {
			pub := firstNonEmpty(it.PubDate, it.DCDate)
			if pub == "" {
				// Namensraum-Varianten wie ercc:pubDate
				for _, o := range it.Other {
					if l := strings.ToLower(o.XMLName.Local); l == "pubdate" || l == "date" || l == "published" {
						pub = strings.TrimSpace(o.Text)
						break
					}
				}
			}
			cats := []string{}
			for _, c := range it.Categories {
				if t := strings.TrimSpace(c); t != "" {
					cats = append(cats, t)
				}
			}
			link := strings.TrimSpace(it.Link)
			items = append(items, map[string]any{
				"title":       Snippet(it.Title),
				"pubDate":     pub,
				"link":        link,
				"guid":        firstNonEmpty(it.GUID, link),
				"author":      firstNonEmpty(it.Author, it.Creator),
				"thumbnail":   strings.TrimSpace(it.Enclosure.URL),
				"description": Snippet(it.Description),
				"content":     firstNonEmpty(it.Content, it.Description),
				"categories":  cats,
			})
		}
	}
	return map[string]any{"status": "ok", "feed": feed, "items": items}, nil
}

// ParseFeedJSON: ParseFeed als JSON-Bytes.
func ParseFeedJSON(raw []byte, feedURL string) ([]byte, error) {
	out, err := ParseFeed(raw, feedURL)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
