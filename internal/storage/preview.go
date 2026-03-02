package storage

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// ExtractPreview parses the <head> of an HTML file and extracts social/visual
// metadata into a Preview. Returns a zero Preview if the file doesn't exist or
// can't be parsed.
func ExtractPreview(contentDir, indexFile string) Preview {
	f, err := os.Open(filepath.Join(contentDir, indexFile))
	if err != nil {
		return Preview{}
	}
	defer f.Close()

	var (
		p             Preview
		titleTag      string
		metaDesc      string
		appleTouchHref string
		inTitle       bool
	)

	z := html.NewTokenizer(f)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return finalize(p, titleTag, metaDesc, appleTouchHref, contentDir)

		case html.StartTagToken, html.SelfClosingTagToken:
			tn, hasAttr := z.TagName()
			tag := string(tn)

			if tag == "body" {
				return finalize(p, titleTag, metaDesc, appleTouchHref, contentDir)
			}

			if tag == "title" {
				inTitle = true
				continue
			}

			if !hasAttr {
				continue
			}

			attrs := collectAttrs(z)

			if tag == "meta" {
				prop := attrs["property"]
				name := attrs["name"]
				content := attrs["content"]

				if content != "" {
					switch prop {
					case "og:title":
						p.Title = content
					case "og:description":
						p.Description = content
					case "og:image":
						p.OGImage = content
					}
					if name == "description" && metaDesc == "" {
						metaDesc = content
					}
				}
			}

			if tag == "link" {
				rel := strings.ToLower(attrs["rel"])
				href := attrs["href"]
				if rel == "icon" && p.Favicon == "" {
					p.Favicon = href
				}
				if rel == "apple-touch-icon" && appleTouchHref == "" {
					appleTouchHref = href
				}
			}

		case html.TextToken:
			if inTitle {
				titleTag += string(z.Text())
			}

		case html.EndTagToken:
			tn, _ := z.TagName()
			if string(tn) == "title" {
				inTitle = false
			}
		}
	}
}

// finalize applies fallback logic and returns the final Preview.
func finalize(p Preview, titleTag, metaDesc, appleTouchHref, contentDir string) Preview {
	if p.Title == "" {
		p.Title = strings.TrimSpace(titleTag)
	}
	if p.Description == "" {
		p.Description = metaDesc
	}
	if p.Favicon == "" {
		p.Favicon = appleTouchHref
	}
	if p.Favicon == "" {
		if _, err := os.Stat(filepath.Join(contentDir, "favicon.ico")); err == nil {
			p.Favicon = "/favicon.ico"
		}
	}
	return p
}

// collectAttrs reads all attributes from the current token into a map.
func collectAttrs(z *html.Tokenizer) map[string]string {
	attrs := make(map[string]string)
	for {
		key, val, more := z.TagAttr()
		if k := string(key); k != "" {
			attrs[k] = string(val)
		}
		if !more {
			break
		}
	}
	return attrs
}
