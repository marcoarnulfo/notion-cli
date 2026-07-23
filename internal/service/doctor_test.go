package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func findCheck(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestDoctorReportsAHealthySetup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track","type":"bot"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	for _, c := range checks {
		if c.Status == "fail" {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
}

func TestDoctorReportsAnInvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	}))
	defer srv.Close()

	checks := New(notion.New("bad", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "token")
	if !ok || c.Status != "fail" {
		t.Fatalf("token check = %+v", c)
	}
}

func TestDoctorSpotsARenamedProperty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			// "Stato" is gone; "Status" took its place.
			w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
				"Name":{"name":"Name","type":"title","title":{}},
				"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
				"Status":{"name":"Status","type":"status","status":{"options":[{"name":"Fatto"}]}}
			}}`))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "properties")
	if !ok || c.Status != "fail" {
		t.Fatalf("properties check = %+v", c)
	}
	// The whole value of this check is naming the likely replacement.
	if !strings.Contains(c.Detail, "Stato") || !strings.Contains(c.Detail, "Status") {
		t.Errorf("detail does not point at the rename: %s", c.Detail)
	}
}

func TestDoctorListsDuplicateTickets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[` + rowJSON + `,` + rowJSON + `],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "duplicates")
	if !ok || c.Status != "fail" {
		t.Fatalf("duplicates check = %+v", c)
	}
	if !strings.Contains(c.Detail, "BDF-231") || !strings.Contains(c.Detail, "https://notion.so/page1") {
		t.Errorf("detail does not identify the duplicates: %s", c.Detail)
	}
}
