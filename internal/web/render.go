package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zuoqixiang/accountbook/internal/store"
)

//go:embed assets
var assetsFS embed.FS

type Base struct {
	Title   string
	Page    string
	Book    store.Book
	Books   []store.Book
	Month   string
	Today   string
	Version string
	OK      string
	Err     string
}

var pageFiles = []string{
	"overview", "tx_list", "tx_form", "projects", "project_form",
	"project_detail", "reports", "settings", "error",
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"money":       store.FormatMoney,
		"datecn":      dateCN,
		"monthlabel":  store.MonthLabel,
		"addmonth":    store.MonthAdd,
		"weekdaycn":   weekdayCN,
		"pct":         pctInt,
		"pcttext":     pctText,
		"share":       share,
		"statuslabel": statusLabel,
		"statustone":  statusTone,
		"add":         func(a, b int64) int64 { return a + b },
		"sub":         func(a, b int64) int64 { return a - b },
		"neg":         func(a int64) bool { return a < 0 },
		"or": func(a, b string) string {
			if a != "" {
				return a
			}
			return b
		},
		"amount": func(c int64) string { return strconv.FormatFloat(store.Yuan(c), 'f', 2, 64) },
		"pid": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
	}
}

func mustTemplates() map[string]*template.Template {
	f := templateFuncs()
	m := make(map[string]*template.Template, len(pageFiles))
	for _, p := range pageFiles {
		t, err := template.New("layout").Funcs(f).ParseFS(assetsFS,
			"assets/templates/layout.html",
			"assets/templates/partials.html",
			"assets/templates/"+p+".html",
		)
		if err != nil {
			panic(fmt.Sprintf("parse template %s: %v", p, err))
		}
		m[p] = t
	}
	return m
}

func dateCN(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%d月%d日", int(t.Month()), t.Day())
}

func weekdayCN(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return ""
	}
	return []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[int(t.Weekday())]
}

func pctInt(cur, prev int64) int {
	if prev == 0 {
		if cur == 0 {
			return 0
		}
		return 100
	}
	p := int(math.Round(float64(cur-prev) / math.Abs(float64(prev)) * 100))
	if p > 999 {
		p = 999
	}
	if p < -999 {
		p = -999
	}
	return p
}

// pctText 环比百分比文案；极端值不虚报具体数字。
func pctText(cur, prev int64) string {
	if prev == 0 {
		return "—"
	}
	p := int(math.Round(float64(cur-prev) / math.Abs(float64(prev)) * 100))
	if p > 999 {
		return ">999"
	}
	if p < -999 {
		return "<-999"
	}
	return strconv.Itoa(p)
}

// share 返回 a 占 b 的百分比（0–100 及以上，无正负号）。
func share(a, b int64) int {
	if b == 0 {
		return 0
	}
	return int(math.Round(float64(a) / float64(b) * 100))
}

func statusLabel(s string) string {
	switch s {
	case "signed":
		return "已签单"
	case "working":
		return "施工中"
	case "done":
		return "已完工"
	case "settled":
		return "已结算"
	}
	return "未知"
}

func statusTone(s string) string {
	switch s {
	case "working":
		return "accent"
	case "done":
		return "ok"
	case "settled":
		return "muted"
	}
	return ""
}

func (s *Server) base(r *http.Request, page, title string) Base {
	books, err := s.st.Books()
	if err != nil {
		log.Printf("list books: %v", err)
	}
	book := store.Book{Name: "未命名账本", ID: 0}
	if len(books) > 0 {
		book = books[0]
	}
	if c, err := r.Cookie("book"); err == nil {
		if id, err := strconv.ParseInt(c.Value, 10, 64); err == nil {
			for _, b := range books {
				if b.ID == id {
					book = b
					break
				}
			}
		}
	}
	return Base{
		Title:   title,
		Page:    page,
		Book:    book,
		Books:   books,
		Month:   store.ThisMonth(),
		Today:   store.Today(),
		Version: s.version,
		OK:      okMessage(r.URL.Query().Get("ok")),
	}
}

func okMessage(code string) string {
	switch code {
	case "saved":
		return "已保存"
	case "deleted":
		return "已删除"
	case "book":
		return "已切换账本"
	case "backup":
		return "备份完成，保留最近 7 份"
	case "import":
		return "已完成"
	}
	return ""
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t, ok := s.tpl[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) staticFS() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets/static")
	if err != nil {
		panic(err)
	}
	return sub
}

// asset 读取嵌入的静态文件。
func asset(name string) ([]byte, error) {
	return assetsFS.ReadFile("assets/static/" + name)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json") ||
		r.Header.Get("X-Requested-With") == "fetch"
}
