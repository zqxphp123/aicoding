package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zuoqixiang/accountbook/internal/store"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "book.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	srv, err := New(st, dir, "test")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		st.Close()
	})
	return srv, ts
}

func noRedirectClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func post(t *testing.T, c *http.Client, ts *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	res, err := c.PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return res
}

func get(t *testing.T, c *http.Client, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	res, err := c.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return res
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func mainBookID(t *testing.T, srv *Server) int64 {
	t.Helper()
	books, err := srv.st.Books()
	if err != nil || len(books) == 0 {
		t.Fatalf("books: %v", err)
	}
	return books[0].ID
}

func TestTransactionCRUD(t *testing.T) {
	srv, ts := newTestServer(t)
	c := noRedirectClient(t)
	bookID := mainBookID(t, srv)

	res := post(t, c, ts, "/tx", url.Values{
		"direction": {"out"}, "amount": {"12.34"}, "occurred_on": {"2026-11-03"},
		"category_id": {"1"}, "counterparty": {"面馆"}, "pay_method": {"微信"}, "note": {"午餐"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status = %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/tx?month=2026-11&ok=saved" {
		t.Fatalf("create location = %q", loc)
	}
	res.Body.Close()

	list, sum, err := srv.st.ListTransactions(bookID, store.Filters{Month: "2026-11"})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d, err %v", len(list), err)
	}
	if list[0].Amount != 1234 || sum.Out != 1234 {
		t.Fatalf("amount = %d, sum.out = %d", list[0].Amount, sum.Out)
	}
	id := list[0].ID

	edit := body(t, get(t, c, ts, "/tx/"+itoa(id)+"/edit"))
	if !strings.Contains(edit, `value="12.34"`) {
		t.Fatalf("edit page missing amount: %s", edit[:200])
	}
	if !strings.Contains(edit, "午餐") {
		t.Fatalf("edit page missing note")
	}

	res = post(t, c, ts, "/tx/"+itoa(id), url.Values{
		"direction": {"out"}, "amount": {"20"}, "occurred_on": {"2026-11-04"},
		"category_id": {"1"}, "note": {"晚餐"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("update status = %d", res.StatusCode)
	}
	res.Body.Close()

	tx, err := srv.st.TxByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Amount != 2000 || tx.Note != "晚餐" || tx.OccurredOn != "2026-11-04" {
		t.Fatalf("updated tx = %+v", tx)
	}

	res = post(t, c, ts, "/tx/"+itoa(id)+"/delete", url.Values{"month": {"2026-11"}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status = %d", res.StatusCode)
	}
	res.Body.Close()

	if _, err := srv.st.TxByID(id); err == nil {
		t.Fatal("deleted tx still readable")
	}
	list, _, _ = srv.st.ListTransactions(bookID, store.Filters{Month: "2026-11"})
	if len(list) != 0 {
		t.Fatalf("after delete list = %d", len(list))
	}
}

func TestTransactionMonthFilter(t *testing.T) {
	_, ts := newTestServer(t)
	c := noRedirectClient(t)
	for _, m := range []string{"2026-10-31", "2026-11-01"} {
		res := post(t, c, ts, "/tx", url.Values{
			"direction": {"in"}, "amount": {"100"}, "occurred_on": {m},
		})
		res.Body.Close()
	}
	html := body(t, get(t, c, ts, "/tx?month=2026-11"))
	if !strings.Contains(html, "共 1 笔") {
		t.Fatalf("month filter count missing: %s", excerpt(html, "共"))
	}
	if strings.Contains(html, "2026-10-31") {
		t.Fatal("october tx leaked into november")
	}
	oct := body(t, get(t, c, ts, "/tx?month=2026-10"))
	if !strings.Contains(oct, "共 1 笔") {
		t.Fatal("october count wrong")
	}
}

func TestUploadAndVoucher(t *testing.T) {
	srv, ts := newTestServer(t)
	c := noRedirectClient(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "receipt.png")
	png.Encode(fw, tinyPNG())
	mw.Close()

	res, err := c.Post(ts.URL+"/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, body %s", res.StatusCode, body(t, res))
	}
	var up struct {
		Key string `json:"key"`
		URL string `json:"url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&up); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	res.Body.Close()
	if !strings.HasSuffix(up.Key, ".jpg") || !strings.HasPrefix(up.Key, "20") {
		t.Fatalf("bad upload key %q", up.Key)
	}

	media := get(t, c, ts, "/media/"+up.Key)
	if media.StatusCode != http.StatusOK {
		t.Fatalf("media status = %d", media.StatusCode)
	}
	if ct := media.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/jpeg") {
		t.Fatalf("media content-type = %q", ct)
	}
	media.Body.Close()

	var form bytes.Buffer
	mw = multipart.NewWriter(&form)
	mw.WriteField("direction", "out")
	mw.WriteField("amount", "88.80")
	mw.WriteField("occurred_on", "2026-11-06")
	mw.WriteField("category_id", "1")
	mw.WriteField("note", "有凭证")
	mw.WriteField("attach", up.Key)
	mw.Close()

	res, err = c.Post(ts.URL+"/tx", mw.FormDataContentType(), &form)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create with voucher status = %d", res.StatusCode)
	}
	res.Body.Close()

	bookID := mainBookID(t, srv)
	list, _, _ := srv.st.ListTransactions(bookID, store.Filters{Month: "2026-11"})
	if len(list) != 1 || list[0].AttachmentCnt != 1 {
		t.Fatalf("attachments = %+v", list)
	}
	edit := body(t, get(t, c, ts, "/tx/"+itoa(list[0].ID)+"/edit"))
	if !strings.Contains(edit, "/media/"+up.Key) {
		t.Fatal("edit page missing voucher thumbnail")
	}
}

func TestCSVExport(t *testing.T) {
	srv, ts := newTestServer(t)
	c := noRedirectClient(t)
	bookID := mainBookID(t, srv)

	if _, err := srv.st.CreateTx(store.Tx{BookID: bookID, Direction: "out", Amount: 1234, OccurredOn: "2026-11-03", Note: "午餐"}); err != nil {
		t.Fatal(err)
	}

	res := get(t, c, ts, "/export.csv?month=2026-11")
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("csv missing BOM")
	}
	text := string(raw)
	for _, want := range []string{"日期", "2026-11-03", "12.34", "午餐"} {
		if !strings.Contains(text, want) {
			t.Fatalf("csv missing %q:\n%s", want, text)
		}
	}
}

func TestBookSwitch(t *testing.T) {
	srv, ts := newTestServer(t)
	c := noRedirectClient(t)
	books, _ := srv.st.Books()
	if len(books) < 2 {
		t.Fatal("expected two seeded books")
	}

	res := post(t, c, ts, "/settings/book", url.Values{"book_id": {itoa(books[1].ID)}})
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch status = %d", res.StatusCode)
	}
	if len(res.Cookies()) == 0 || res.Cookies()[0].Name != "book" {
		t.Fatal("book cookie not set")
	}

	form := body(t, get(t, c, ts, "/tx/new"))
	if !strings.Contains(form, "客户回款") {
		t.Fatal("project book categories missing after switch")
	}
	if strings.Contains(form, ">餐饮<") {
		t.Fatal("personal categories leaked into project book")
	}
}

func TestProjectCRUD(t *testing.T) {
	srv, ts := newTestServer(t)
	c := noRedirectClient(t)
	bookID := mainBookID(t, srv)

	res := post(t, c, ts, "/projects", url.Values{
		"name": {"测试项目"}, "status": {"working"}, "client_name": {"陈先生"},
		"contract_amount": {"268000"}, "manager_amount": {"180000"},
		"start_date": {"2026-10-08"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create project status = %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	res.Body.Close()
	if !strings.HasPrefix(loc, "/projects/") {
		t.Fatalf("project location = %q", loc)
	}

	projs, _ := srv.st.Projects(bookID)
	if len(projs) != 1 || projs[0].ContractAmount != 26800000 {
		t.Fatalf("projects = %+v", projs)
	}
	p := projs[0]

	detail := body(t, get(t, c, ts, "/projects/"+itoa(p.ID)))
	if !strings.Contains(detail, "¥268,000.00") || !strings.Contains(detail, "陈先生") {
		t.Fatal("detail missing contract amount or client")
	}

	res = post(t, c, ts, "/projects/"+itoa(p.ID), url.Values{
		"name": {"改名项目"}, "status": {"done"}, "contract_amount": {"300000"},
	})
	res.Body.Close()
	p2, _ := srv.st.ProjectByID(p.ID)
	if p2.Name != "改名项目" || p2.Status != "done" || p2.ContractAmount != 30000000 {
		t.Fatalf("updated project = %+v", p2)
	}

	res = post(t, c, ts, "/projects/"+itoa(p.ID)+"/delete", nil)
	res.Body.Close()
	if _, err := srv.st.ProjectByID(p.ID); err == nil {
		t.Fatal("project still readable after delete")
	}
}

func TestValidationErrors(t *testing.T) {
	_, ts := newTestServer(t)
	c := noRedirectClient(t)

	for _, form := range []url.Values{
		{"direction": {"out"}, "amount": {"abc"}, "occurred_on": {"2026-11-03"}},
		{"direction": {"out"}, "amount": {"0"}, "occurred_on": {"2026-11-03"}},
		{"direction": {"nope"}, "amount": {"10"}, "occurred_on": {"2026-11-03"}},
		{"direction": {"out"}, "amount": {"10"}, "occurred_on": {"2026-13-40"}},
	} {
		res := post(t, c, ts, "/tx", form)
		html := body(t, res)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("validation status = %d", res.StatusCode)
		}
		if !strings.Contains(html, `class="alert"`) {
			t.Fatalf("validation error not shown for %v", form)
		}
	}
}

func tinyPNG() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{15, 118, 110, 255})
		}
	}
	return img
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func excerpt(s, key string) string {
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	end := i + 80
	if end > len(s) {
		end = len(s)
	}
	return s[i:end]
}
