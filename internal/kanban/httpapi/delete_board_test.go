package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/base698/amythest/internal/kanban/board"
)

func loginForDelete(t *testing.T, baseURL string) (*http.Cookie, string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == SessionCookie {
			return cookie, payload.CSRF
		}
	}
	t.Fatal("session cookie missing")
	return nil, ""
}

func deleteBoardRequest(t *testing.T, baseURL, name string, cookie *http.Cookie, csrf string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/api/boards/"+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDeleteBoardAPIRequiresAuthCSRFAndEmptyBoard(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	if _, err := store.CreateBoard("temporary"); err != nil {
		t.Fatal(err)
	}
	resp := deleteBoardRequest(t, srv.URL, "temporary", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delete = %d", resp.StatusCode)
	}
	resp.Body.Close()
	cookie, csrf := loginForDelete(t, srv.URL)
	resp = deleteBoardRequest(t, srv.URL, "temporary", cookie, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without csrf = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = deleteBoardRequest(t, srv.URL, "temporary", cookie, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty delete = %d", resp.StatusCode)
	}
	resp.Body.Close()

	if _, err := store.CreateBoard("kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCard("kept", board.CardInput{Title: "Keep"}); err != nil {
		t.Fatal(err)
	}
	resp = deleteBoardRequest(t, srv.URL, "kept", cookie, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-empty delete = %d", resp.StatusCode)
	}
	resp.Body.Close()
}
