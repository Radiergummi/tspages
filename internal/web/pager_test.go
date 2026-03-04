package web

import "testing"

func TestPager_PrevURL(t *testing.T) {
	p := Pager{Page: 3, TotalPages: 5, BaseURL: "/deployments?foo=bar"}
	if got := p.PrevURL(); got != "/deployments?foo=bar&page=2" {
		t.Errorf("PrevURL = %q", got)
	}
}

func TestPager_NextURL(t *testing.T) {
	p := Pager{Page: 3, TotalPages: 5, BaseURL: "/deployments?foo=bar"}
	if got := p.NextURL(); got != "/deployments?foo=bar&page=4" {
		t.Errorf("NextURL = %q", got)
	}
}

func TestPager_HasPrev(t *testing.T) {
	if (Pager{Page: 1}).HasPrev() {
		t.Error("page 1 should not have prev")
	}
	if !(Pager{Page: 2}).HasPrev() {
		t.Error("page 2 should have prev")
	}
}

func TestPager_HasNext(t *testing.T) {
	if (Pager{Page: 5, TotalPages: 5}).HasNext() {
		t.Error("last page should not have next")
	}
	if !(Pager{Page: 3, TotalPages: 5}).HasNext() {
		t.Error("page 3/5 should have next")
	}
}

func TestPager_Visible(t *testing.T) {
	if (Pager{Page: 1, TotalPages: 1}).Visible() {
		t.Error("single page should not be visible")
	}
	if !(Pager{Page: 1, TotalPages: 2}).Visible() {
		t.Error("2-page pager should be visible")
	}
}
