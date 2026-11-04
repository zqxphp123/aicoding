package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store 是全部持久化访问的入口。SQLite 单文件，WAL 模式。
type Store struct {
	DB   *sql.DB
	Path string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{DB: db, Path: path}
	if err := s.seed(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) seed() error {
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM books`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := s.CreateBook("个人账本", "personal"); err != nil {
		return err
	}
	if _, err := s.CreateBook("工程账本", "project"); err != nil {
		return err
	}
	return nil
}

// ---------- 账本 ----------

type Book struct {
	ID        int64
	Name      string
	Kind      string
	Currency  string
	CreatedAt string
}

func (s *Store) Books() ([]Book, error) {
	rows, err := s.DB.Query(`SELECT id, name, kind, currency, created_at FROM books ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Book
	for rows.Next() {
		var b Book
		if err := rows.Scan(&b.ID, &b.Name, &b.Kind, &b.Currency, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) BookByID(id int64) (Book, error) {
	var b Book
	err := s.DB.QueryRow(`SELECT id, name, kind, currency, created_at FROM books WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Kind, &b.Currency, &b.CreatedAt)
	return b, err
}

func (s *Store) CreateBook(name, kind string) (int64, error) {
	now := nowStr()
	res, err := s.DB.Exec(`INSERT INTO books(name, kind, currency, created_at) VALUES(?,?,?,?)`,
		name, kind, "CNY", now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	seed := defaultCategories(kind)
	for _, c := range seed {
		if _, err := s.DB.Exec(`INSERT INTO categories(book_id, direction, name, sort, is_system) VALUES(?,?,?,?,1)`,
			id, c.Direction, c.Name, c.Sort); err != nil {
			return 0, err
		}
	}
	return id, nil
}

type seedCat struct {
	Direction string
	Name      string
	Sort      int
}

func defaultCategories(kind string) []seedCat {
	personal := []seedCat{
		{"out", "餐饮", 10}, {"out", "交通", 20}, {"out", "居住", 30}, {"out", "购物", 40},
		{"out", "医疗", 50}, {"out", "学习", 60}, {"out", "娱乐", 70}, {"out", "人情", 80},
		{"out", "通讯", 90}, {"out", "其他", 100},
		{"in", "工资", 10}, {"in", "兼职", 20}, {"in", "投资", 30}, {"in", "报销", 40}, {"in", "其他", 50},
	}
	project := []seedCat{
		{"out", "材料", 10}, {"out", "人工", 20}, {"out", "运输", 30}, {"out", "工具", 40},
		{"out", "招待", 50}, {"out", "税费", 60}, {"out", "其他", 70},
		{"in", "客户回款", 10}, {"in", "其他", 20},
	}
	if kind == "project" {
		return project
	}
	return personal
}

// ---------- 分类 ----------

type Category struct {
	ID        int64
	BookID    int64
	Direction string
	Name      string
	Sort      int
}

func (s *Store) Categories(bookID int64) ([]Category, error) {
	rows, err := s.DB.Query(`SELECT id, book_id, direction, name, sort FROM categories WHERE book_id=? ORDER BY direction DESC, sort, id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.BookID, &c.Direction, &c.Name, &c.Sort); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddCategory(bookID int64, direction, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("分类名称不能为空")
	}
	if direction != "in" && direction != "out" {
		return errors.New("direction 只能是 in 或 out")
	}
	var maxSort int
	s.DB.QueryRow(`SELECT coalesce(max(sort),0) FROM categories WHERE book_id=? AND direction=?`, bookID, direction).Scan(&maxSort)
	_, err := s.DB.Exec(`INSERT INTO categories(book_id, direction, name, sort, is_system) VALUES(?,?,?,?,0)`,
		bookID, direction, name, maxSort+10)
	return err
}

// ---------- 项目 ----------

type Project struct {
	ID             int64
	BookID         int64
	Name           string
	ClientName     string
	ContractAmount int64
	Designer       string
	ManagerName    string
	ManagerAmount  int64
	StartDate      string
	PlannedEndDate string
	ActualEndDate  string
	Status         string
	Note           string
	CreatedAt      string
	UpdatedAt      string
	In             int64
	Out            int64
}

func (p Project) Profit() int64 { return p.In - p.Out }

func (s *Store) Projects(bookID int64) ([]Project, error) {
	rows, err := s.DB.Query(`
		SELECT p.id, p.book_id, p.name, p.client_name, p.contract_amount, p.designer, p.manager_name,
		       p.manager_amount, p.start_date, p.planned_end_date, p.actual_end_date, p.status, p.note,
		       p.created_at, p.updated_at,
		       coalesce(sum(CASE WHEN t.direction='in' THEN t.amount ELSE 0 END),0) AS in_sum,
		       coalesce(sum(CASE WHEN t.direction='out' THEN t.amount ELSE 0 END),0) AS out_sum
		FROM projects p
		LEFT JOIN transactions t ON t.project_id = p.id AND t.deleted_at IS NULL
		WHERE p.book_id=? AND p.deleted_at IS NULL
		GROUP BY p.id
		ORDER BY CASE p.status WHEN 'working' THEN 0 WHEN 'signed' THEN 1 WHEN 'done' THEN 2 ELSE 3 END, p.id DESC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.BookID, &p.Name, &p.ClientName, &p.ContractAmount, &p.Designer,
			&p.ManagerName, &p.ManagerAmount, &p.StartDate, &p.PlannedEndDate, &p.ActualEndDate,
			&p.Status, &p.Note, &p.CreatedAt, &p.UpdatedAt, &p.In, &p.Out); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProjectByID(id int64) (Project, error) {
	var p Project
	err := s.DB.QueryRow(`
		SELECT p.id, p.book_id, p.name, p.client_name, p.contract_amount, p.designer, p.manager_name,
		       p.manager_amount, p.start_date, p.planned_end_date, p.actual_end_date, p.status, p.note,
		       p.created_at, p.updated_at,
		       coalesce(sum(CASE WHEN t.direction='in' THEN t.amount ELSE 0 END),0),
		       coalesce(sum(CASE WHEN t.direction='out' THEN t.amount ELSE 0 END),0)
		FROM projects p
		LEFT JOIN transactions t ON t.project_id = p.id AND t.deleted_at IS NULL
		WHERE p.id=? AND p.deleted_at IS NULL
		GROUP BY p.id`, id).
		Scan(&p.ID, &p.BookID, &p.Name, &p.ClientName, &p.ContractAmount, &p.Designer, &p.ManagerName,
			&p.ManagerAmount, &p.StartDate, &p.PlannedEndDate, &p.ActualEndDate, &p.Status, &p.Note,
			&p.CreatedAt, &p.UpdatedAt, &p.In, &p.Out)
	return p, err
}

func (s *Store) CreateProject(p Project) (int64, error) {
	now := nowStr()
	res, err := s.DB.Exec(`INSERT INTO projects(book_id, name, client_name, contract_amount, designer,
		manager_name, manager_amount, start_date, planned_end_date, actual_end_date, status, note, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.BookID, p.Name, p.ClientName, p.ContractAmount, p.Designer, p.ManagerName, p.ManagerAmount,
		p.StartDate, p.PlannedEndDate, p.ActualEndDate, p.Status, p.Note, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateProject(p Project) error {
	_, err := s.DB.Exec(`UPDATE projects SET name=?, client_name=?, contract_amount=?, designer=?,
		manager_name=?, manager_amount=?, start_date=?, planned_end_date=?, actual_end_date=?, status=?,
		note=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		p.Name, p.ClientName, p.ContractAmount, p.Designer, p.ManagerName, p.ManagerAmount,
		p.StartDate, p.PlannedEndDate, p.ActualEndDate, p.Status, p.Note, nowStr(), p.ID)
	return err
}

func (s *Store) DeleteProject(id int64) error {
	_, err := s.DB.Exec(`UPDATE projects SET deleted_at=? WHERE id=?`, nowStr(), id)
	return err
}

// ---------- 流水 ----------

type Attachment struct {
	ID        int64
	ObjectKey string
	Mime      string
	Size      int64
}

type Tx struct {
	ID            int64
	BookID        int64
	ProjectID     *int64
	ProjectName   string
	Direction     string
	CategoryID    *int64
	CategoryName  string
	Amount        int64
	OccurredOn    string
	Counterparty  string
	PayMethod     string
	Note          string
	CreatedAt     string
	AttachmentCnt int
	Attachments   []Attachment
}

type Filters struct {
	Month      string // YYYY-MM，空表示不限
	Direction  string // in / out / ""
	ProjectID  int64
	CategoryID int64
	Q          string
	NoProof    bool
	Limit      int
}

type Summary struct {
	In  int64
	Out int64
}

func (s Summary) Net() int64 { return s.In - s.Out }

func (f Filters) where(bookID int64) (string, []any) {
	parts := []string{"t.book_id=?", "t.deleted_at IS NULL"}
	args := []any{bookID}
	if f.Month != "" {
		parts = append(parts, "substr(t.occurred_on,1,7)=?")
		args = append(args, f.Month)
	}
	if f.Direction == "in" || f.Direction == "out" {
		parts = append(parts, "t.direction=?")
		args = append(args, f.Direction)
	}
	if f.ProjectID > 0 {
		parts = append(parts, "t.project_id=?")
		args = append(args, f.ProjectID)
	}
	if f.CategoryID > 0 {
		parts = append(parts, "t.category_id=?")
		args = append(args, f.CategoryID)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		parts = append(parts, "(t.note LIKE ? OR t.counterparty LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	if f.NoProof {
		parts = append(parts, "NOT EXISTS (SELECT 1 FROM attachments a WHERE a.transaction_id=t.id)")
	}
	return strings.Join(parts, " AND "), args
}

func (s *Store) ListTransactions(bookID int64, f Filters) ([]Tx, Summary, error) {
	where, args := f.where(bookID)
	limit := ""
	if f.Limit > 0 {
		limit = fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	q := `SELECT t.id, t.book_id, t.project_id, coalesce(p.name,''), t.direction, t.category_id,
	             coalesce(c.name,''), t.amount, t.occurred_on, t.counterparty, t.pay_method, t.note,
	             t.created_at,
	             (SELECT count(*) FROM attachments a WHERE a.transaction_id=t.id)
	      FROM transactions t
	      LEFT JOIN categories c ON c.id=t.category_id
	      LEFT JOIN projects p ON p.id=t.project_id
	      WHERE ` + where + `
	      ORDER BY t.occurred_on DESC, t.id DESC` + limit
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, Summary{}, err
	}
	defer rows.Close()
	var out []Tx
	for rows.Next() {
		var t Tx
		var pid, cid sql.NullInt64
		if err := rows.Scan(&t.ID, &t.BookID, &pid, &t.ProjectName, &t.Direction, &cid, &t.CategoryName,
			&t.Amount, &t.OccurredOn, &t.Counterparty, &t.PayMethod, &t.Note, &t.CreatedAt, &t.AttachmentCnt); err != nil {
			return nil, Summary{}, err
		}
		if pid.Valid {
			t.ProjectID = &pid.Int64
		}
		if cid.Valid {
			t.CategoryID = &cid.Int64
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, Summary{}, err
	}

	var sum Summary
	agg := `SELECT coalesce(sum(CASE WHEN t.direction='in' THEN t.amount ELSE 0 END),0),
	               coalesce(sum(CASE WHEN t.direction='out' THEN t.amount ELSE 0 END),0)
	        FROM transactions t WHERE ` + where
	if err := s.DB.QueryRow(agg, args...).Scan(&sum.In, &sum.Out); err != nil {
		return nil, Summary{}, err
	}
	return out, sum, nil
}

func (s *Store) TxByID(id int64) (Tx, error) {
	var t Tx
	var pid, cid sql.NullInt64
	err := s.DB.QueryRow(`SELECT t.id, t.book_id, t.project_id, coalesce(p.name,''), t.direction, t.category_id,
		coalesce(c.name,''), t.amount, t.occurred_on, t.counterparty, t.pay_method, t.note, t.created_at,
		(SELECT count(*) FROM attachments a WHERE a.transaction_id=t.id)
		FROM transactions t
		LEFT JOIN categories c ON c.id=t.category_id
		LEFT JOIN projects p ON p.id=t.project_id
		WHERE t.id=? AND t.deleted_at IS NULL`, id).
		Scan(&t.ID, &t.BookID, &pid, &t.ProjectName, &t.Direction, &cid, &t.CategoryName,
			&t.Amount, &t.OccurredOn, &t.Counterparty, &t.PayMethod, &t.Note, &t.CreatedAt, &t.AttachmentCnt)
	if err != nil {
		return t, err
	}
	if pid.Valid {
		t.ProjectID = &pid.Int64
	}
	if cid.Valid {
		t.CategoryID = &cid.Int64
	}
	att, err := s.Attachments(t.ID)
	if err != nil {
		return t, err
	}
	t.Attachments = att
	return t, nil
}

func (s *Store) CreateTx(t Tx) (int64, error) {
	now := nowStr()
	res, err := s.DB.Exec(`INSERT INTO transactions(book_id, project_id, direction, category_id, amount,
		occurred_on, counterparty, pay_method, note, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		t.BookID, nullInt(t.ProjectID), t.Direction, nullInt(t.CategoryID), t.Amount,
		t.OccurredOn, t.Counterparty, t.PayMethod, t.Note, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateTx(t Tx) error {
	_, err := s.DB.Exec(`UPDATE transactions SET project_id=?, direction=?, category_id=?, amount=?,
		occurred_on=?, counterparty=?, pay_method=?, note=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		nullInt(t.ProjectID), t.Direction, nullInt(t.CategoryID), t.Amount,
		t.OccurredOn, t.Counterparty, t.PayMethod, t.Note, nowStr(), t.ID)
	return err
}

func (s *Store) DeleteTx(id int64) error {
	_, err := s.DB.Exec(`UPDATE transactions SET deleted_at=?, updated_at=? WHERE id=?`, nowStr(), nowStr(), id)
	return err
}

func (s *Store) Attachments(txID int64) ([]Attachment, error) {
	rows, err := s.DB.Query(`SELECT id, object_key, mime, size FROM attachments WHERE transaction_id=? ORDER BY id`, txID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.ObjectKey, &a.Mime, &a.Size); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AddAttachment(txID int64, key, mime string, size int64) error {
	_, err := s.DB.Exec(`INSERT INTO attachments(transaction_id, object_key, mime, size, created_at) VALUES(?,?,?,?,?)`,
		txID, key, mime, size, nowStr())
	return err
}

func (s *Store) DeleteAttachment(id int64) (string, error) {
	var key string
	if err := s.DB.QueryRow(`SELECT object_key FROM attachments WHERE id=?`, id).Scan(&key); err != nil {
		return "", err
	}
	_, err := s.DB.Exec(`DELETE FROM attachments WHERE id=?`, id)
	return key, err
}

// ---------- 报表 ----------

func (s *Store) MonthlySummary(bookID int64, month string) (Summary, error) {
	var sum Summary
	err := s.DB.QueryRow(`SELECT coalesce(sum(CASE WHEN direction='in' THEN amount ELSE 0 END),0),
		coalesce(sum(CASE WHEN direction='out' THEN amount ELSE 0 END),0)
		FROM transactions WHERE book_id=? AND deleted_at IS NULL AND substr(occurred_on,1,7)=?`,
		bookID, month).Scan(&sum.In, &sum.Out)
	return sum, err
}

type MonthPoint struct {
	Month string
	In    int64
	Out   int64
}

// MonthlyTrend 返回截至 endMonth（含）的最近 n 个月。
func (s *Store) MonthlyTrend(bookID int64, endMonth string, n int) ([]MonthPoint, error) {
	months := make([]string, 0, n)
	cur, err := time.Parse("2006-01", endMonth)
	if err != nil {
		return nil, err
	}
	for i := n - 1; i >= 0; i-- {
		months = append(months, cur.AddDate(0, -i, 0).Format("2006-01"))
	}
	start := months[0]
	rows, err := s.DB.Query(`SELECT substr(occurred_on,1,7), direction, sum(amount)
		FROM transactions
		WHERE book_id=? AND deleted_at IS NULL AND substr(occurred_on,1,7) >= ?
		GROUP BY 1, 2`, bookID, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agg := map[string]*MonthPoint{}
	for rows.Next() {
		var m, dir string
		var amt int64
		if err := rows.Scan(&m, &dir, &amt); err != nil {
			return nil, err
		}
		if agg[m] == nil {
			agg[m] = &MonthPoint{Month: m}
		}
		if dir == "in" {
			agg[m].In = amt
		} else {
			agg[m].Out = amt
		}
	}
	out := make([]MonthPoint, 0, n)
	for _, m := range months {
		if p := agg[m]; p != nil {
			out = append(out, *p)
		} else {
			out = append(out, MonthPoint{Month: m})
		}
	}
	return out, rows.Err()
}

type CatSum struct {
	Name   string
	Amount int64
}

func (s *Store) CategoryBreakdown(bookID int64, month, direction string) ([]CatSum, error) {
	rows, err := s.DB.Query(`SELECT coalesce(c.name,'未分类'), sum(t.amount)
		FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
		WHERE t.book_id=? AND t.deleted_at IS NULL AND t.direction=? AND substr(t.occurred_on,1,7)=?
		GROUP BY c.id ORDER BY 2 DESC`, bookID, direction, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatSum
	for rows.Next() {
		var c CatSum
		if err := rows.Scan(&c.Name, &c.Amount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ProjectRanking(bookID int64, limit int) ([]Project, error) {
	list, err := s.Projects(bookID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Profit() > list[j].Profit() })
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (s *Store) PendingVoucherCount(bookID int64, month string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT count(*) FROM transactions t
		WHERE t.book_id=? AND t.deleted_at IS NULL AND t.direction='out' AND substr(t.occurred_on,1,7)=?
		AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.transaction_id=t.id)`, bookID, month).Scan(&n)
	return n, err
}

// ---------- 备份 ----------

func (s *Store) Backup(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "book-" + time.Now().Format("20060102-150405") + ".db"
	path := filepath.Join(dir, name)
	if _, err := s.DB.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err == nil {
		var dbs []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), "book-") && strings.HasSuffix(e.Name(), ".db") {
				dbs = append(dbs, e.Name())
			}
		}
		sort.Strings(dbs)
		for len(dbs) > 7 {
			os.Remove(filepath.Join(dir, dbs[0]))
			dbs = dbs[1:]
		}
	}
	return path, nil
}

// ---------- 工具 ----------

func nowStr() string { return time.Now().Format(time.RFC3339) }

func nullInt(p *int64) any {
	if p == nil || *p == 0 {
		return nil
	}
	return *p
}

// ParseAmount 把用户输入解析成「分」。支持 1,234.56 / ￥12 / 0.5 等写法。
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer(",", "", "，", "", "￥", "", "¥", "", "元", "", " ", "").Replace(s)
	if s == "" {
		return 0, errors.New("请输入金额")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	if len(parts) == 2 && len(parts[1]) > 2 {
		return 0, errors.New("金额最多两位小数")
	}
	intPart, fracPart := parts[0], ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}
	if intPart == "" {
		intPart = "0"
	}
	for _, r := range intPart + fracPart {
		if r < '0' || r > '9' {
			return 0, errors.New("金额格式不正确")
		}
	}
	yuan, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, errors.New("金额过大")
	}
	var fen int64
	switch len(fracPart) {
	case 0:
		fen = 0
	case 1:
		f, _ := strconv.ParseInt(fracPart, 10, 64)
		fen = f * 10
	case 2:
		f, _ := strconv.ParseInt(fracPart, 10, 64)
		fen = f
	}
	total := yuan*100 + fen
	if neg {
		total = -total
	}
	if total > 99_999_999_999 {
		return 0, errors.New("金额过大")
	}
	return total, nil
}

// FormatMoney 把「分」格式化成 1,234.56（不带货币符号）。
func FormatMoney(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	yuan := cents / 100
	fen := cents % 100
	whole := strconv.FormatInt(yuan, 10)
	var b strings.Builder
	n := len(whole)
	for i, ch := range whole {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := fmt.Sprintf("%s.%02d", b.String(), fen)
	if neg {
		return "-" + out
	}
	return out
}

// Yuan 返回浮点元，供图表使用（图表只需展示，允许浮点）。
func Yuan(cents int64) float64 {
	return math.Round(float64(cents)) / 100
}

// MonthAdd 返回 month（YYYY-MM）加减 n 个月的结果。
func MonthAdd(month string, n int) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return t.AddDate(0, n, 0).Format("2006-01")
}

// MonthLabel 把 2026-11 变成 2026年11月。
func MonthLabel(month string) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return fmt.Sprintf("%d年%d月", t.Year(), int(t.Month()))
}

func Today() string { return time.Now().Format("2006-01-02") }

func ThisMonth() string { return time.Now().Format("2006-01") }
