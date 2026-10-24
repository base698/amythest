package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/base698/amythest/internal/kanban/auth"
	"github.com/base698/amythest/internal/kanban/board"
)

func testServer(t *testing.T) (*httptest.Server, *board.Store) {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC) }
	store := board.NewStore(t.TempDir(), clock)
	if _, err := store.CreateBoard("proof"); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager("operator", "correct horse", []byte("01234567890123456789012345678901"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": {Data: []byte("<html><body>kanban app</body></html>")}}
	handler := New(Config{Store: store, Auth: manager, Assets: assets, Now: clock})
	return httptest.NewServer(handler), store
}

func TestAPIRequiresLoginAndCSRFThenCreatesCard(t *testing.T) {
	srv, _ := testServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/boards/proof")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", resp.StatusCode)
	}

	loginBody := bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`)
	resp, err = http.Post(srv.URL+"/api/login", "application/json", loginBody)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly {
		t.Fatal("secure session cookie missing")
	}

	payload := bytes.NewBufferString(`{"title":"Deploy Proof production with login","status":"ready","assignee":"Operator","labels":["proof","1.0"]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/boards/proof/cards", payload)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(sessionCookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", resp.StatusCode)
	}

	payload = bytes.NewBufferString(`{"title":"Deploy project with login","status":"ready","priority":"p1","assignee":"operator","dueDate":"2026-08-04","milestone":"1.0","labels":["release"]}`)
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/boards/proof/cards", payload)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", login.CSRF)
	req.AddCookie(sessionCookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	var created board.Card
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.DueDate != "2026-08-04" {
		t.Fatalf("created due date = %q", created.DueDate)
	}
	if created.Milestone != "1.0" {
		t.Fatalf("created milestone = %q", created.Milestone)
	}
	if created.Priority != board.P1 {
		t.Fatalf("created priority = %q", created.Priority)
	}
}

func TestCreateCardAPIImplicitFourthBoardIsUnpinned(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	pinned := true
	for _, name := range []string{"second", "third"} {
		if _, err := store.CreateBoardWithInput(board.BoardInput{Name: name, Pinned: &pinned}); err != nil {
			t.Fatal(err)
		}
	}

	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer loginResp.Body.Close()
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/boards/fourth/cards", bytes.NewBufferString(`{"title":"Implicit card","status":"triage"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", login.CSRF)
	req.AddCookie(loginResp.Cookies()[0])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create card status = %d body=%s", resp.StatusCode, readResponse(t, resp))
	}
	created, err := store.Load("fourth")
	if err != nil {
		t.Fatal(err)
	}
	if created.Pinned || len(created.Cards) != 1 || created.Cards[0].Title != "Implicit card" {
		t.Fatalf("implicit board = %#v", created)
	}
}

func TestCreateBoardAPIRequiresAuthenticationAndCSRFAndRejectsDuplicateOrInvalidNames(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()

	create := func(cookie *http.Cookie, csrf, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/boards", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
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
	if got := create(nil, "", `{"name":"new-project"}`).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = %d", got)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	cookie := loginResp.Cookies()[0]
	if got := create(cookie, "", `{"name":"new-project"}`).StatusCode; got != http.StatusForbidden {
		t.Fatalf("create without CSRF = %d", got)
	}
	resp := create(cookie, login.CSRF, `{"name":"new-project","displayName":"New Project","description":"Scoped work","icon":"folder","color":"#336699","sortOrder":40,"pinned":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	var created board.Board
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "new-project" || created.DisplayName != "New Project" || created.Description != "Scoped work" || created.Icon != "folder" || created.Color != "#336699" || created.SortOrder != 40 || !created.Pinned || created.DispatchEnabled {
		t.Fatalf("created board = %#v", created)
	}
	if _, err := store.Load("new-project"); err != nil {
		t.Fatalf("created board was not persisted: %v", err)
	}
	if got := create(cookie, login.CSRF, `{"name":"new-project"}`).StatusCode; got != http.StatusConflict {
		t.Fatalf("duplicate create = %d", got)
	}
	if got := create(cookie, login.CSRF, `{"name":"../escape"}`).StatusCode; got != http.StatusBadRequest {
		t.Fatalf("invalid create = %d", got)
	}
}

func TestCreateFavoriteAPIReturnsGenericInternalErrorForStrictReadFailure(t *testing.T) {
	root := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC) }
	store := board.NewStore(root, clock)
	if _, err := store.CreateBoard("existing"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing", "board.md"), []byte("not valid board data"), 0o640); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager("operator", "correct horse", []byte("01234567890123456789012345678901"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": {Data: []byte("<html><body>kanban app</body></html>")}}
	srv := httptest.NewServer(New(Config{Store: store, Auth: manager, Assets: assets, Now: clock}))
	defer srv.Close()

	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer loginResp.Body.Close()
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/boards", bytes.NewBufferString(`{"name":"candidate","pinned":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", login.CSRF)
	req.AddCookie(loginResp.Cookies()[0])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readResponse(t, resp)
	if resp.StatusCode != http.StatusInternalServerError || body != `{"error":"internal server error"}`+"\n" {
		t.Fatalf("strict read failure response = %d %q", resp.StatusCode, body)
	}
}

func TestMoveCardAPIRequiresCSRFAndPersistsRequestedPosition(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	first, _ := store.CreateCard("proof", board.CardInput{Title: "First", Status: board.Ready})
	second, _ := store.CreateCard("proof", board.CardInput{Title: "Second", Status: board.Ready})

	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/api/boards/proof/cards/" + second.ID + "/move"
	request := func(csrf string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(`{"status":"ready","beforeId":"`+first.ID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(loginResp.Cookies()[0])
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return resp
	}
	if got := request("").StatusCode; got != http.StatusForbidden {
		t.Fatalf("move without CSRF = %d", got)
	}
	if got := request(login.CSRF).StatusCode; got != http.StatusOK {
		t.Fatalf("move = %d", got)
	}
	loaded, err := store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cards) != 2 || loaded.Cards[0].ID != second.ID || loaded.Cards[1].ID != first.ID {
		t.Fatalf("persisted API order = %#v", loaded.Cards)
	}
}

func TestMoveCardToBoardAPIRequiresAuthenticationCSRFAndExplicitConfirmation(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	if err := store.EnsureBoard("destination"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("proof", board.CardInput{Title: "Move between boards", Status: board.Ready})
	if err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/api/boards/proof/cards/" + card.ID + "/board"
	request := func(cookie *http.Cookie, csrf, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return resp
	}
	if got := request(nil, "", `{"destinationBoard":"destination","confirm":true}`).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated move = %d", got)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	cookie := loginResp.Cookies()[0]
	if got := request(cookie, "", `{"destinationBoard":"destination","confirm":true}`).StatusCode; got != http.StatusForbidden {
		t.Fatalf("move without CSRF = %d", got)
	}
	if got := request(cookie, login.CSRF, `{"destinationBoard":"destination","confirm":false}`).StatusCode; got != http.StatusBadRequest {
		t.Fatalf("move without explicit confirmation = %d", got)
	}
	resp := request(cookie, login.CSRF, `{"destinationBoard":"destination","confirm":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed move = %d body=%s", resp.StatusCode, readResponse(t, resp))
	}
	var moved board.Card
	if err := json.NewDecoder(resp.Body).Decode(&moved); err != nil {
		t.Fatal(err)
	}
	if moved.ID != card.ID || len(moved.Audit) != 1 || moved.Audit[0].Actor != "operator" {
		t.Fatalf("moved card = %#v", moved)
	}
	source, _ := store.Load("proof")
	destination, _ := store.Load("destination")
	if len(source.Cards) != 0 || len(destination.Cards) != 1 || destination.Cards[0].ID != card.ID {
		t.Fatalf("source=%#v destination=%#v", source.Cards, destination.Cards)
	}
}

func TestDeleteCardRequiresAuthenticationAndCSRF(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	card, err := store.CreateCard("proof", board.CardInput{Title: "Misplaced", Status: board.Ready})
	if err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/api/boards/proof/cards/" + card.ID
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delete = %d", resp.StatusCode)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	cookie := loginResp.Cookies()[0]
	req, _ = http.NewRequest(http.MethodDelete, url, nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without CSRF = %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, url, nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", login.CSRF)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	value, err := store.Load("proof")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Cards) != 0 {
		t.Fatalf("cards after delete = %#v", value.Cards)
	}
}

func TestArchiveSearchAndRestoreRequireAuthenticationAndCSRF(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	card, err := store.CreateCard("proof", board.CardInput{Title: "Family Photo Builder", Status: board.Ready})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateCard("proof", card.ID, board.CardPatch{Status: ptrBoardStatus(board.Done)}); err != nil {
		t.Fatal(err)
	}
	archiveURL := srv.URL + "/api/boards/proof/archive?q=fam+pho"
	resp, err := http.Get(archiveURL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated archive search = %d", resp.StatusCode)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	cookie := loginResp.Cookies()[0]
	req, _ := http.NewRequest(http.MethodGet, archiveURL, nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var archived []board.Card
	if err := json.NewDecoder(resp.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0].ID != card.ID || archived[0].DoneAt == nil {
		t.Fatalf("archive response = %#v", archived)
	}
	restoreURL := srv.URL + "/api/boards/proof/archive/" + card.ID + "/restore"
	req, _ = http.NewRequest(http.MethodPost, restoreURL, bytes.NewBufferString(`{"status":"triage"}`))
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("restore without CSRF = %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, restoreURL, bytes.NewBufferString(`{"status":"triage"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", login.CSRF)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore = %d", resp.StatusCode)
	}
	value, err := store.Load("proof")
	if err != nil || len(value.Cards) != 1 || value.Cards[0].Status != board.Triage {
		t.Fatalf("active after restore = %#v err=%v", value.Cards, err)
	}
}

func TestAttachmentAPIRequiresAuthenticationAndCSRFAndSupportsUploadListDownloadDelete(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	card, err := store.CreateCard("proof", board.CardInput{Title: "Evidence", Status: board.Ready})
	if err != nil {
		t.Fatal(err)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	cookie := loginResp.Cookies()[0]
	baseURL := srv.URL + "/api/boards/proof/cards/" + card.ID + "/attachments"

	upload := func(withCookie bool, csrf string) *http.Response {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, partErr := writer.CreateFormFile("file", "../evidence.pdf")
		if partErr != nil {
			t.Fatal(partErr)
		}
		_, _ = part.Write([]byte("%PDF-1.4 evidence"))
		_ = writer.Close()
		req, _ := http.NewRequest(http.MethodPost, baseURL, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("X-CSRF-Token", csrf)
		if withCookie {
			req.AddCookie(cookie)
		}
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return resp
	}
	if got := upload(false, "").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload=%d", got)
	}
	if got := upload(true, "").StatusCode; got != http.StatusForbidden {
		t.Fatalf("upload without CSRF=%d", got)
	}
	resp := upload(true, login.CSRF)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload=%d body=%s", resp.StatusCode, readResponse(t, resp))
	}
	var attachment board.Attachment
	if err := json.NewDecoder(resp.Body).Decode(&attachment); err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != "evidence.pdf" || attachment.ContentType != "application/pdf" {
		t.Fatalf("attachment=%#v", attachment)
	}

	req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var listed []board.Attachment
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil || len(listed) != 1 || listed[0].ID != attachment.ID {
		t.Fatalf("list=%#v err=%v", listed, err)
	}

	downloadURL := baseURL + "/" + attachment.ID
	resp, err = http.Get(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated download=%d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, downloadURL, nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(data) != "%PDF-1.4 evidence" {
		t.Fatalf("download=%d %q", resp.StatusCode, data)
	}
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename=evidence.pdf` {
		t.Fatalf("disposition=%q", got)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("download headers=%v", resp.Header)
	}

	req, _ = http.NewRequest(http.MethodDelete, downloadURL, nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without CSRF=%d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodDelete, downloadURL, nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", login.CSRF)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete=%v err=%v", resp.StatusCode, err)
	}
}

func TestPatchBoardSettingsRequiresCSRFAndPersistsFocus(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	card, err := store.CreateCard("proof", board.CardInput{Title: "Current focus", Status: board.Ready})
	if err != nil {
		t.Fatal(err)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	request := func(csrf, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/boards/proof/settings", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(loginResp.Cookies()[0])
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return resp
	}
	body := `{"displayName":"Proof Product","description":"Product work","icon":"check","color":"#228855","sortOrder":60,"pinned":true,"focusCardId":"` + card.ID + `"}`
	if got := request("", body).StatusCode; got != http.StatusForbidden {
		t.Fatalf("settings without CSRF = %d", got)
	}
	resp := request(login.CSRF, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings = %d body=%s", resp.StatusCode, readResponse(t, resp))
	}
	var updated board.Board
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Proof Product" || updated.FocusCardID != card.ID || updated.SortOrder != 60 {
		t.Fatalf("updated settings = %#v", updated)
	}
	if got := request(login.CSRF, `{"focusCardId":"k_missing"}`).StatusCode; got != http.StatusBadRequest {
		t.Fatalf("invalid focus = %d", got)
	}
}

func TestConcurrentFavoriteRequestsCannotExceedLimit(t *testing.T) {
	for _, operation := range []string{"create", "settings"} {
		t.Run(operation, func(t *testing.T) {
			srv, store := testServer(t)
			defer srv.Close()
			pinned, unpinned := true, false
			if _, err := store.CreateBoardWithInput(board.BoardInput{Name: "second", Pinned: &pinned}); err != nil {
				t.Fatal(err)
			}
			if operation == "settings" {
				for _, name := range []string{"candidate-a", "candidate-b"} {
					if _, err := store.CreateBoardWithInput(board.BoardInput{Name: name, Pinned: &unpinned}); err != nil {
						t.Fatal(err)
					}
				}
			}

			loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
			if err != nil {
				t.Fatal(err)
			}
			var login struct {
				CSRF string `json:"csrf"`
			}
			if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
				t.Fatal(err)
			}
			_ = loginResp.Body.Close()
			cookie := loginResp.Cookies()[0]
			start := make(chan struct{})
			type result struct {
				status int
				body   string
			}
			results := make(chan result, 2)
			for _, name := range []string{"candidate-a", "candidate-b"} {
				go func(name string) {
					<-start
					method, url, payload := http.MethodPost, srv.URL+"/api/boards", `{"name":"`+name+`","pinned":true}`
					if operation == "settings" {
						method, url, payload = http.MethodPatch, srv.URL+"/api/boards/"+name+"/settings", `{"pinned":true}`
					}
					req, _ := http.NewRequest(method, url, bytes.NewBufferString(payload))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-CSRF-Token", login.CSRF)
					req.AddCookie(cookie)
					resp, requestErr := http.DefaultClient.Do(req)
					if requestErr != nil {
						results <- result{}
						return
					}
					body, readErr := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if readErr != nil {
						results <- result{}
						return
					}
					results <- result{status: resp.StatusCode, body: string(body)}
				}(name)
			}
			close(start)
			counts := map[int]int{}
			var rejectionBody string
			for range 2 {
				result := <-results
				counts[result.status]++
				if result.status == http.StatusBadRequest {
					rejectionBody = result.body
				}
			}
			wantSuccess := http.StatusOK
			if operation == "create" {
				wantSuccess = http.StatusCreated
			}
			if counts[wantSuccess] != 1 || counts[http.StatusBadRequest] != 1 {
				t.Fatalf("concurrent %s statuses = %#v", operation, counts)
			}
			wantBody := `{"error":"at most three active boards may be favorites"}` + "\n"
			if rejectionBody != wantBody {
				t.Fatalf("concurrent %s rejection body = %q, want %q", operation, rejectionBody, wantBody)
			}
		})
	}
}

func TestMoveArchivedCardToBoardAPIRequiresConfirmation(t *testing.T) {
	srv, store := testServer(t)
	defer srv.Close()
	if _, err := store.CreateBoard("destination"); err != nil {
		t.Fatal(err)
	}
	card, err := store.CreateCard("proof", board.CardInput{Title: "Archived evidence", Status: board.Ready})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveCard("proof", card.ID, board.Done, ""); err != nil {
		t.Fatal(err)
	}
	loginResp, err := http.Post(srv.URL+"/api/login", "application/json", bytes.NewBufferString(`{"username":"operator","password":"correct horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	request := func(confirm bool) *http.Response {
		payload := fmt.Sprintf(`{"destinationBoard":"destination","confirm":%t}`, confirm)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/boards/proof/archive/"+card.ID+"/board", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", login.CSRF)
		req.AddCookie(loginResp.Cookies()[0])
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return resp
	}
	if got := request(false).StatusCode; got != http.StatusBadRequest {
		t.Fatalf("archive move without confirmation = %d", got)
	}
	resp := request(true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive move = %d body=%s", resp.StatusCode, readResponse(t, resp))
	}
	from, _ := store.ListArchived("proof", "", 100)
	to, _ := store.ListArchived("destination", "", 100)
	if len(from) != 0 || len(to) != 1 || to[0].ID != card.ID {
		t.Fatalf("source=%#v destination=%#v", from, to)
	}
}

func readResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	value, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func ptrBoardStatus(status board.Status) *board.Status { return &status }

func TestHostRouterServesDemoOnlyForPreviewHost(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("app")) })
	previewFS := fstest.MapFS{"index.html": {Data: []byte("demo")}}
	preview := http.FileServer(http.FS(previewFS))
	handler := HostRouter(app, preview, []string{"demo-preview.example"}, false)

	for host, want := range map[string]string{"kanban.example": "app", "demo-preview.example:8792": "demo"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		req.Host = host
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Body.String() != want {
			t.Fatalf("host %s body = %q, want %q", host, rr.Body.String(), want)
		}
	}
}

func TestLoginFailureMapPrunesExpiredEntriesAndStaysBounded(t *testing.T) {
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	s := &server{config: Config{Now: func() time.Time { return now }}, failures: map[string]loginFailures{}}
	for i := 0; i < maxLoginFailureEntries+50; i++ {
		s.failures[string(rune(i+1))] = loginFailures{Count: 1, Reset: now.Add(-time.Minute)}
	}
	s.recordLoginFailure("current")
	if len(s.failures) != 1 {
		t.Fatalf("expired failure entries were not pruned: %d remain", len(s.failures))
	}
	for i := 0; i < maxLoginFailureEntries+50; i++ {
		s.recordLoginFailure(fmt.Sprintf("active-%d", i))
	}
	if len(s.failures) > maxLoginFailureEntries {
		t.Fatalf("failure map grew to %d entries", len(s.failures))
	}
	if _, ok := s.failures["active-1073"]; !ok {
		t.Fatal("most recent failure was not retained")
	}
}

var _ fs.FS
