package web

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zuoqixiang/accountbook/internal/store"
)

type Server struct {
	st      *store.Store
	tpl     map[string]*template.Template
	dataDir string
	uploads string
	version string
}

func New(st *store.Store, dataDir, version string) (*Server, error) {
	uploads := filepath.Join(dataDir, "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		return nil, err
	}
	return &Server{
		st:      st,
		tpl:     mustTemplates(),
		dataDir: dataDir,
		uploads: uploads,
		version: version,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.overview)

	mux.HandleFunc("GET /tx", s.txList)
	mux.HandleFunc("GET /tx/new", s.txNew)
	mux.HandleFunc("POST /tx", s.txCreate)
	mux.HandleFunc("GET /tx/{id}/edit", s.txEdit)
	mux.HandleFunc("POST /tx/{id}", s.txUpdate)
	mux.HandleFunc("POST /tx/{id}/delete", s.txDelete)

	mux.HandleFunc("GET /projects", s.projects)
	mux.HandleFunc("GET /projects/new", s.projectNew)
	mux.HandleFunc("POST /projects", s.projectCreate)
	mux.HandleFunc("GET /projects/{id}", s.projectDetail)
	mux.HandleFunc("GET /projects/{id}/edit", s.projectEdit)
	mux.HandleFunc("POST /projects/{id}", s.projectUpdate)
	mux.HandleFunc("POST /projects/{id}/delete", s.projectDelete)

	mux.HandleFunc("GET /reports", s.reports)
	mux.HandleFunc("GET /export.csv", s.exportCSV)

	mux.HandleFunc("GET /settings", s.settings)
	mux.HandleFunc("POST /settings/book", s.switchBook)
	mux.HandleFunc("POST /settings/book/new", s.createBook)
	mux.HandleFunc("POST /settings/category", s.createCategory)
	mux.HandleFunc("POST /settings/backup", s.backup)

	mux.HandleFunc("POST /upload", s.upload)
	mux.HandleFunc("GET /media/{key...}", s.media)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(s.staticFS())))
	mux.HandleFunc("GET /manifest.webmanifest", s.serveAsset("manifest.webmanifest", "application/manifest+json"))
	mux.HandleFunc("GET /sw.js", s.swJS)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/icons/icon-192.png", http.StatusFound)
	})

	return recoverMW(logMW(mux))
}

func (s *Server) serveAsset(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := asset(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	}
}

// swJS 输出 service worker，并把缓存名绑定到当前版本：
// 每次升级版本，旧缓存会自动失效，避免用户拿到过期前端。
func (s *Server) swJS(w http.ResponseWriter, r *http.Request) {
	b, err := asset("sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(bytes.ReplaceAll(b, []byte("__VERSION__"), []byte(s.version)))
}

// ---------- 概览 ----------

type overviewData struct {
	Base
	Summary  store.Summary
	Prev     store.Summary
	Pending  int
	Cats     []store.CatSum
	CatJSON  string
	Recent   []store.Tx
	Projects []store.Project
	Month    string
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "overview", "概览")
	month := r.URL.Query().Get("month")
	if !validMonth(month) {
		month = store.ThisMonth()
	}
	b.Month = month

	sum, err := s.st.MonthlySummary(b.Book.ID, month)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	prev, _ := s.st.MonthlySummary(b.Book.ID, store.MonthAdd(month, -1))
	pending, _ := s.st.PendingVoucherCount(b.Book.ID, month)
	cats, _ := s.st.CategoryBreakdown(b.Book.ID, month, "out")
	if len(cats) > 5 {
		cats = cats[:5]
	}
	recent, _, _ := s.st.ListTransactions(b.Book.ID, store.Filters{Limit: 6})
	projs, _ := s.st.ProjectRanking(b.Book.ID, 3)

	data := overviewData{
		Base: b, Summary: sum, Prev: prev, Pending: pending,
		Cats: cats, Recent: recent, Projects: projs, Month: month,
	}
	if catJSON, err := json.Marshal(donutSpec(cats)); err == nil {
		data.CatJSON = string(catJSON)
	}
	s.render(w, "overview", data)
}

// ---------- 流水 ----------

type dayGroup struct {
	Date string
	Txs  []store.Tx
	In   int64
	Out  int64
}

type txListData struct {
	Base
	Txs        []store.Tx
	Groups     []dayGroup
	Summary    store.Summary
	Filters    store.Filters
	Categories []store.Category
	Projects   []store.Project
	Month      string
	Count      int
}

func (s *Server) txList(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "tx", "流水")
	q := r.URL.Query()
	month := q.Get("month")
	if !validMonth(month) {
		month = store.ThisMonth()
	}
	b.Month = month

	f := store.Filters{
		Month:     month,
		Direction: q.Get("dir"),
		Q:         q.Get("q"),
		NoProof:   q.Get("noproof") == "1",
	}
	if v, err := strconv.ParseInt(q.Get("project"), 10, 64); err == nil {
		f.ProjectID = v
	}
	if v, err := strconv.ParseInt(q.Get("cat"), 10, 64); err == nil {
		f.CategoryID = v
	}
	txs, sum, err := s.st.ListTransactions(b.Book.ID, f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	groups := []dayGroup{}
	for _, t := range txs {
		if len(groups) == 0 || groups[len(groups)-1].Date != t.OccurredOn {
			groups = append(groups, dayGroup{Date: t.OccurredOn})
		}
		g := &groups[len(groups)-1]
		g.Txs = append(g.Txs, t)
		if t.Direction == "in" {
			g.In += t.Amount
		} else {
			g.Out += t.Amount
		}
	}
	cats, _ := s.st.Categories(b.Book.ID)
	projs, _ := s.st.Projects(b.Book.ID)
	s.render(w, "tx_list", txListData{
		Base: b, Txs: txs, Groups: groups, Summary: sum, Filters: f,
		Categories: cats, Projects: projs, Month: month, Count: len(txs),
	})
}

type txFormData struct {
	Base
	Tx         store.Tx
	IsEdit     bool
	Categories []store.Category
	Projects   []store.Project
	PMethods   []string
	Month      string
}

var payMethods = []string{"微信", "支付宝", "银行卡", "现金", "其他"}

func (s *Server) txNew(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "tx", "记一笔")
	cats, _ := s.st.Categories(b.Book.ID)
	projs, _ := s.st.Projects(b.Book.ID)
	tx := store.Tx{Direction: "out", OccurredOn: store.Today(), BookID: b.Book.ID}
	if v, err := strconv.ParseInt(r.URL.Query().Get("project"), 10, 64); err == nil && v > 0 {
		tx.ProjectID = &v
	}
	s.render(w, "tx_form", txFormData{
		Base: b, Tx: tx, Categories: cats, Projects: projs, PMethods: payMethods,
		Month: tx.OccurredOn[:7],
	})
}

func (s *Server) txEdit(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "tx", "编辑流水")
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	tx, err := s.st.TxByID(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	cats, _ := s.st.Categories(tx.BookID)
	projs, _ := s.st.Projects(tx.BookID)
	s.render(w, "tx_form", txFormData{
		Base: b, Tx: tx, IsEdit: true, Categories: cats, Projects: projs,
		PMethods: payMethods, Month: tx.OccurredOn[:7],
	})
}

func (s *Server) txCreate(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "tx", "记一笔")
	tx, ferr := s.parseTxForm(r, b.Book.ID)
	if ferr != nil {
		s.renderTxFormError(w, r, b, tx, false, ferr)
		return
	}
	id, err := s.st.CreateTx(tx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.saveUploadedFiles(r, id); err != nil {
		log.Printf("save uploads: %v", err)
	}
	s.attachKeys(r, id)
	http.Redirect(w, r, "/tx?month="+tx.OccurredOn[:7]+"&ok=saved", http.StatusSeeOther)
}

func (s *Server) txUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	cur, err := s.st.TxByID(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	b := s.base(r, "tx", "编辑流水")
	tx, ferr := s.parseTxForm(r, cur.BookID)
	if ferr != nil {
		tx.ID = id
		s.renderTxFormError(w, r, b, tx, true, ferr)
		return
	}
	tx.ID = id
	if err := s.st.UpdateTx(tx); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.MultipartForm != nil {
		for _, v := range r.MultipartForm.Value["del_attach"] {
			aid, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				continue
			}
			if key, err := s.st.DeleteAttachment(aid); err == nil {
				os.Remove(filepath.Join(s.uploads, filepath.FromSlash(key)))
			}
		}
	}
	if err := s.saveUploadedFiles(r, id); err != nil {
		log.Printf("save uploads: %v", err)
	}
	s.attachKeys(r, id)
	s.attachKeys(r, id)
	http.Redirect(w, r, "/tx?month="+tx.OccurredOn[:7]+"&ok=saved", http.StatusSeeOther)
}

func (s *Server) renderTxFormError(w http.ResponseWriter, r *http.Request, b Base, tx store.Tx, isEdit bool, ferr error) {
	cats, _ := s.st.Categories(tx.BookID)
	projs, _ := s.st.Projects(tx.BookID)
	b.Err = ferr.Error()
	month := tx.OccurredOn
	if len(month) >= 7 {
		month = month[:7]
	} else {
		month = store.ThisMonth()
	}
	s.render(w, "tx_form", txFormData{
		Base: b, Tx: tx, IsEdit: isEdit, Categories: cats, Projects: projs,
		PMethods: payMethods, Month: month,
	})
}

func (s *Server) parseTxForm(r *http.Request, bookID int64) (store.Tx, error) {
	var tx store.Tx
	tx.BookID = bookID
	if err := r.ParseMultipartForm(64 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return tx, errors.New("表单解析失败")
	}
	tx.Direction = r.FormValue("direction")
	if tx.Direction != "in" && tx.Direction != "out" {
		return tx, errors.New("请选择收入或支出")
	}
	amt, err := store.ParseAmount(r.FormValue("amount"))
	if err != nil {
		return tx, err
	}
	if amt <= 0 {
		return tx, errors.New("金额必须大于 0")
	}
	tx.Amount = amt
	on := strings.TrimSpace(r.FormValue("occurred_on"))
	if _, err := time.Parse("2006-01-02", on); err != nil {
		return tx, errors.New("请选择正确的日期")
	}
	tx.OccurredOn = on
	if v, err := strconv.ParseInt(r.FormValue("category_id"), 10, 64); err == nil && v > 0 {
		tx.CategoryID = &v
	}
	if v, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64); err == nil && v > 0 {
		p, err := s.st.ProjectByID(v)
		if err != nil || p.BookID != bookID {
			return tx, errors.New("项目不存在")
		}
		tx.ProjectID = &v
	}
	tx.Counterparty = strings.TrimSpace(r.FormValue("counterparty"))
	tx.PayMethod = strings.TrimSpace(r.FormValue("pay_method"))
	tx.Note = strings.TrimSpace(r.FormValue("note"))
	return tx, nil
}

func (s *Server) txDelete(w http.ResponseWriter, r *http.Request) {
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	month := r.FormValue("month")
	if err := s.st.DeleteTx(id); err != nil {
		s.fail(w, r, err)
		return
	}
	if !validMonth(month) {
		month = store.ThisMonth()
	}
	http.Redirect(w, r, "/tx?month="+month+"&ok=deleted", http.StatusSeeOther)
}

// ---------- 项目 ----------

type projectsData struct {
	Base
	Projects []store.Project
	TotalIn  int64
	TotalOut int64
	Active   int
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "projects", "项目")
	list, err := s.st.Projects(b.Book.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in, out int64
	active := 0
	for _, p := range list {
		in += p.In
		out += p.Out
		if p.Status == "working" || p.Status == "signed" {
			active++
		}
	}
	s.render(w, "projects", projectsData{Base: b, Projects: list, TotalIn: in, TotalOut: out, Active: active})
}

type projectFormData struct {
	Base
	Project store.Project
	IsEdit  bool
}

func (s *Server) projectNew(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "projects", "新建项目")
	s.render(w, "project_form", projectFormData{
		Base:    b,
		Project: store.Project{BookID: b.Book.ID, Status: "working", StartDate: store.Today()},
	})
}

func (s *Server) projectEdit(w http.ResponseWriter, r *http.Request) {
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	p, err := s.st.ProjectByID(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	b := s.base(r, "projects", "编辑项目")
	s.render(w, "project_form", projectFormData{Base: b, Project: p, IsEdit: true})
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "projects", "新建项目")
	p, ferr := s.parseProjectForm(r, b.Book.ID)
	if ferr != nil {
		b.Err = ferr.Error()
		s.render(w, "project_form", projectFormData{Base: b, Project: p})
		return
	}
	id, err := s.st.CreateProject(p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d?ok=saved", id), http.StatusSeeOther)
}

func (s *Server) projectUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	cur, err := s.st.ProjectByID(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	b := s.base(r, "projects", "编辑项目")
	p, ferr := s.parseProjectForm(r, cur.BookID)
	p.ID = id
	if ferr != nil {
		b.Err = ferr.Error()
		s.render(w, "project_form", projectFormData{Base: b, Project: p, IsEdit: true})
		return
	}
	if err := s.st.UpdateProject(p); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d?ok=saved", id), http.StatusSeeOther)
}

func (s *Server) projectDelete(w http.ResponseWriter, r *http.Request) {
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.st.DeleteProject(id); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects?ok=deleted", http.StatusSeeOther)
}

func (s *Server) parseProjectForm(r *http.Request, bookID int64) (store.Project, error) {
	p := store.Project{BookID: bookID}
	if err := r.ParseForm(); err != nil {
		return p, errors.New("表单解析失败")
	}
	p.Name = strings.TrimSpace(r.FormValue("name"))
	if p.Name == "" {
		return p, errors.New("请填写项目名称")
	}
	p.ClientName = strings.TrimSpace(r.FormValue("client_name"))
	p.Designer = strings.TrimSpace(r.FormValue("designer"))
	p.ManagerName = strings.TrimSpace(r.FormValue("manager_name"))
	p.Note = strings.TrimSpace(r.FormValue("note"))
	p.Status = r.FormValue("status")
	switch p.Status {
	case "signed", "working", "done", "settled":
	default:
		p.Status = "working"
	}
	for _, f := range []struct {
		name string
		dst  *int64
	}{
		{"contract_amount", &p.ContractAmount},
		{"manager_amount", &p.ManagerAmount},
	} {
		raw := strings.TrimSpace(r.FormValue(f.name))
		if raw == "" {
			continue
		}
		v, err := store.ParseAmount(raw)
		if err != nil {
			return p, fmt.Errorf("%s：%v", f.name, err)
		}
		*f.dst = v
	}
	for _, f := range []struct {
		name string
		dst  *string
	}{
		{"start_date", &p.StartDate},
		{"planned_end_date", &p.PlannedEndDate},
		{"actual_end_date", &p.ActualEndDate},
	} {
		v := strings.TrimSpace(r.FormValue(f.name))
		if v == "" {
			*f.dst = ""
			continue
		}
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return p, errors.New("日期格式不正确")
		}
		*f.dst = v
	}
	return p, nil
}

type projectDetailData struct {
	Base
	Project store.Project
	Txs     []store.Tx
	Summary store.Summary
}

func (s *Server) projectDetail(w http.ResponseWriter, r *http.Request) {
	id, err := idFrom(r)
	if err != nil {
		s.notFound(w, r)
		return
	}
	p, err := s.st.ProjectByID(id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	b := s.base(r, "projects", p.Name)
	txs, sum, _ := s.st.ListTransactions(p.BookID, store.Filters{ProjectID: p.ID})
	s.render(w, "project_detail", projectDetailData{Base: b, Project: p, Txs: txs, Summary: sum})
}

// ---------- 报表 ----------

type reportsData struct {
	Base
	Month     string
	Summary   store.Summary
	Prev      store.Summary
	Trend     []store.MonthPoint
	TrendJSON string
	Cats      []store.CatSum
	CatJSON   string
	Ranking   []store.Project
}

func (s *Server) reports(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "reports", "报表")
	month := r.URL.Query().Get("month")
	if !validMonth(month) {
		month = store.ThisMonth()
	}
	b.Month = month
	sum, _ := s.st.MonthlySummary(b.Book.ID, month)
	prev, _ := s.st.MonthlySummary(b.Book.ID, store.MonthAdd(month, -1))
	trend, _ := s.st.MonthlyTrend(b.Book.ID, month, 6)
	cats, _ := s.st.CategoryBreakdown(b.Book.ID, month, "out")
	rank, _ := s.st.ProjectRanking(b.Book.ID, 8)

	data := reportsData{Base: b, Month: month, Summary: sum, Prev: prev, Trend: trend, Cats: cats, Ranking: rank}
	labels := make([]string, len(trend))
	in := make([]float64, len(trend))
	out := make([]float64, len(trend))
	for i, p := range trend {
		labels[i] = store.MonthLabel(p.Month)
		in[i] = store.Yuan(p.In)
		out[i] = store.Yuan(p.Out)
	}
	trendSpec := map[string]any{
		"type":   "line",
		"labels": labels,
		"unit":   "元",
		"series": []map[string]any{
			{"name": "收入", "values": in, "color": "#0e7c66"},
			{"name": "支出", "values": out, "color": "#d9584c"},
		},
	}
	if bs, err := json.Marshal(trendSpec); err == nil {
		data.TrendJSON = string(bs)
	}
	if bs, err := json.Marshal(donutSpec(cats)); err == nil {
		data.CatJSON = string(bs)
	}
	s.render(w, "reports", data)
}

func donutSpec(cats []store.CatSum) map[string]any {
	data := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		data = append(data, map[string]any{"label": c.Name, "value": store.Yuan(c.Amount)})
	}
	return map[string]any{"type": "donut", "data": data, "unit": "元"}
}

// ---------- 导出 ----------

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "reports", "导出")
	month := r.URL.Query().Get("month")
	if !validMonth(month) {
		month = store.ThisMonth()
	}
	txs, _, err := s.st.ListTransactions(b.Book.ID, store.Filters{Month: month})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := fmt.Sprintf("%s-%s.csv", b.Book.Name, month)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Write([]string{"日期", "类型", "金额(元)", "分类", "项目", "往来对象", "支付方式", "备注", "凭证数"})
	for _, t := range txs {
		kind := "收入"
		if t.Direction == "out" {
			kind = "支出"
		}
		cw.Write([]string{
			t.OccurredOn, kind, fmt.Sprintf("%d.%02d", t.Amount/100, t.Amount%100),
			t.CategoryName, t.ProjectName, t.Counterparty, t.PayMethod, t.Note,
			strconv.Itoa(t.AttachmentCnt),
		})
	}
	cw.Flush()
}

// ---------- 设置 ----------

type settingsData struct {
	Base
	Cats      []store.Category
	DataDir   string
	TxCount   int
	ProjCount int
	AttCount  int
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "settings", "设置")
	cats, _ := s.st.Categories(b.Book.ID)
	abs, _ := filepath.Abs(s.dataDir)
	var txCount, projCount, attCount int
	s.st.DB.QueryRow(`SELECT count(*) FROM transactions WHERE deleted_at IS NULL`).Scan(&txCount)
	s.st.DB.QueryRow(`SELECT count(*) FROM projects WHERE deleted_at IS NULL`).Scan(&projCount)
	s.st.DB.QueryRow(`SELECT count(*) FROM attachments`).Scan(&attCount)
	s.render(w, "settings", settingsData{
		Base: b, Cats: cats, DataDir: abs, TxCount: txCount, ProjCount: projCount, AttCount: attCount,
	})
}

func (s *Server) switchBook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("book_id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "book", Value: strconv.FormatInt(id, 10), Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	target := "/"
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Path != "" {
			target = u.Path
			if u.RawQuery != "" {
				target += "?" + u.RawQuery
			}
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) createBook(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	if kind != "personal" && kind != "project" {
		kind = "personal"
	}
	if name == "" {
		http.Redirect(w, r, "/settings?ok=", http.StatusSeeOther)
		return
	}
	id, err := s.st.CreateBook(name, kind)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "book", Value: strconv.FormatInt(id, 10), Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/settings?ok=book", http.StatusSeeOther)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "settings", "设置")
	if err := s.st.AddCategory(b.Book.ID, r.FormValue("direction"), r.FormValue("name")); err != nil {
		http.Redirect(w, r, "/settings?ok=", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?ok=saved", http.StatusSeeOther)
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	if _, err := s.st.Backup(filepath.Join(s.dataDir, "backups")); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?ok=backup", http.StatusSeeOther)
}

// attachKeys 处理前端异步上传后带回的 key。
func (s *Server) attachKeys(r *http.Request, txID int64) {
	for _, key := range r.Form["attach"] {
		if !mediaKeyRe.MatchString(key) {
			continue
		}
		var size int64
		if info, err := os.Stat(filepath.Join(s.uploads, filepath.FromSlash(key))); err == nil {
			size = info.Size()
		}
		if err := s.st.AddAttachment(txID, key, "image/jpeg", size); err != nil {
			log.Printf("attach %s: %v", key, err)
		}
	}
}

// ---------- 工具 ----------

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("request %s %s: %v", r.Method, r.URL.Path, err)
	b := s.base(r, "error", "出错了")
	b.Err = err.Error()
	w.WriteHeader(http.StatusInternalServerError)
	s.render(w, "error", b)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	b := s.base(r, "error", "未找到")
	b.Err = "这条记录不存在，可能已被删除。"
	w.WriteHeader(http.StatusNotFound)
	s.render(w, "error", b)
}

func idFrom(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func validMonth(m string) bool {
	if len(m) != 7 {
		return false
	}
	_, err := time.Parse("2006-01", m)
	return err == nil
}

var mediaKeyRe = regexp.MustCompile(`^\d{6}/[a-z0-9]{8,40}\.(jpg|png|webp)$`)

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !mediaKeyRe.MatchString(key) {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.uploads, filepath.FromSlash(key))
	if _, err := os.Stat(p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, p)
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
		}
	})
}

func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("panic: %v", v)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
