package web

import "fmt"

// Pager holds pagination state for templates. Embed it in page data structs
// and call {{template "pagination" .Pager}} in templates.
type Pager struct {
	Page       int    `json:"page"`
	TotalPages int    `json:"total_pages"`
	BaseURL    string `json:"-"`
}

func (p Pager) HasPrev() bool { return p.Page > 1 }
func (p Pager) HasNext() bool { return p.Page < p.TotalPages }
func (p Pager) Visible() bool { return p.TotalPages > 1 }

func (p Pager) PrevURL() string { return p.pageURL(p.Page - 1) }
func (p Pager) NextURL() string { return p.pageURL(p.Page + 1) }

func (p Pager) pageURL(page int) string {
	sep := "?"
	if len(p.BaseURL) > 0 {
		for _, c := range p.BaseURL {
			if c == '?' {
				sep = "&"
				break
			}
		}
	}
	return fmt.Sprintf("%s%spage=%d", p.BaseURL, sep, page)
}
