package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

const (
	defaultSQLDir = "/queries"
	defaultAddr   = ":8080"
)

var (
	templates = template.Must(template.New("pages").Funcs(template.FuncMap{
		"pathEscape": url.PathEscape,
	}).Parse(pageTemplates))
)

type QueryFile struct {
	Name  string
	Title string
	Path  string
}

type ListPageData struct {
	Queries []QueryFile
}

type ViewPageData struct {
	Title   string
	Query   string
	Headers []string
	Rows    [][]string
	Error   string
}

func main() {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	sqlDir := strings.TrimSpace(os.Getenv("SQL_DIR"))
	if sqlDir == "" {
		sqlDir = defaultSQLDir
	}

	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		addr = defaultAddr
	}

	driver, dsn, err := parseDatabaseURL(dbURL)
	if err != nil {
		log.Fatalf("invalid DATABASE_URL: %v", err)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		log.Fatalf("cannot open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("database ping failed: %v", err)
	}

	queryFiles, err := loadQueryFiles(sqlDir)
	if err != nil {
		log.Fatalf("failed to load SQL files: %v", err)
	}

	if len(queryFiles) == 0 {
		log.Printf("warning: no .sql files found in %s", sqlDir)
	}

	queryMap := make(map[string]QueryFile, len(queryFiles))
	for _, q := range queryFiles {
		queryMap[q.Name] = q
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		renderList(w, queryFiles)
	})

	http.HandleFunc("/view/", func(w http.ResponseWriter, r *http.Request) {
		name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/view/"))
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}
		queryFile, ok := queryMap[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		renderView(w, db, queryFile)
	})

	log.Printf("starting sqlview on %s, serving queries from %s", addr, sqlDir)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func parseDatabaseURL(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "sqlite", "sqlite3", "file":
		dsn := u.Path
		if dsn == "" {
			dsn = u.Opaque
		}
		if dsn == "" {
			return "", "", errors.New("sqlite URL must include a path")
		}
		if u.RawQuery != "" {
			dsn += "?" + u.RawQuery
		}
		return "sqlite", dsn, nil
	case "postgres", "postgresql":
		return "postgres", raw, nil
	case "mysql":
		return "mysql", raw, nil
	default:
		return "", "", fmt.Errorf("unsupported database scheme %q", scheme)
	}
}

func loadQueryFiles(dir string) ([]QueryFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var queries []QueryFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(entry.Name())) != ".sql" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if name == "" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		queries = append(queries, QueryFile{
			Name:  name,
			Title: name,
			Path:  path,
		})
	}

	sort.Slice(queries, func(i, j int) bool {
		return queries[i].Name < queries[j].Name
	})
	return queries, nil
}

func renderList(w http.ResponseWriter, queries []QueryFile) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := ListPageData{Queries: queries}
	if err := templates.ExecuteTemplate(w, "list", data); err != nil {
		http.Error(w, fmt.Sprintf("template error: %v", err), http.StatusInternalServerError)
	}
}

func renderView(w http.ResponseWriter, db *sql.DB, queryFile QueryFile) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	queryBytes, err := os.ReadFile(queryFile.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot read query file: %v", err), http.StatusInternalServerError)
		return
	}

	query := strings.TrimSpace(string(queryBytes))
	if query == "" {
		http.Error(w, "query file is empty", http.StatusBadRequest)
		return
	}

	if !isSelectQuery(query) {
		http.Error(w, "only SELECT or WITH queries are permitted", http.StatusBadRequest)
		return
	}

	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		renderViewError(w, queryFile.Name, query, fmt.Sprintf("query execution failed: %v", err))
		return
	}
	defer rows.Close()

	headers, records, err := scanRows(rows)
	if err != nil {
		renderViewError(w, queryFile.Name, query, fmt.Sprintf("failed to read results: %v", err))
		return
	}

	data := ViewPageData{
		Title:   queryFile.Title,
		Query:   query,
		Headers: headers,
		Rows:    records,
	}
	if err := templates.ExecuteTemplate(w, "view", data); err != nil {
		http.Error(w, fmt.Sprintf("template error: %v", err), http.StatusInternalServerError)
	}
}

func renderViewError(w http.ResponseWriter, title, query, message string) {
	data := ViewPageData{
		Title: title,
		Query: query,
		Error: message,
	}
	if err := templates.ExecuteTemplate(w, "view", data); err != nil {
		http.Error(w, fmt.Sprintf("template error: %v", err), http.StatusInternalServerError)
	}
}

func isSelectQuery(query string) bool {
	query = strings.TrimSpace(query)
	query = strings.ToLower(query)
	return strings.HasPrefix(query, "select") || strings.HasPrefix(query, "with")
}

func scanRows(rows *sql.Rows) ([]string, [][]string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	var records [][]string
	for rows.Next() {
		values := make([]interface{}, len(cols))
		scanArgs := make([]interface{}, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return nil, nil, err
		}

		record := make([]string, len(cols))
		for i, v := range values {
			if v == nil {
				record[i] = ""
				continue
			}
			record[i] = fmt.Sprint(v)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, records, nil
}

const pageTemplates = `{{define "layout"}}
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{.Title}}</title>
  <style>
    body { font-family: system-ui, sans-serif; margin: 0; padding: 1.5rem; background: #f6f7fb; color: #111; }
    h1, h2 { margin-top: 0; }
    a { color: #0066cc; text-decoration: none; }
    a:hover { text-decoration: underline; }
    .container { max-width: 1000px; margin: auto; padding: 1rem; background: #fff; border-radius: 14px; box-shadow: 0 10px 30px rgba(0,0,0,.08); }
    table { border-collapse: collapse; width: 100%; margin-top: 1rem; }
    th, td { border: 1px solid #ddd; padding: 0.75rem; text-align: left; }
    th { background: #f4f6fb; }
    pre { background: #f0f3ff; padding: 1rem; overflow-x: auto; border-radius: 8px; }
    .notice { margin-top: 1rem; padding: 1rem; background: #fff4e5; border: 1px solid #ffd8a8; border-radius: 10px; }
    .error { color: #a03838; background: #ffe9e9; border: 1px solid #f5c2c7; padding: 1rem; border-radius: 10px; }
  </style>
</head>
<body>
  <div class="container">
    {{template "content" .}}
  </div>
</body>
</html>
{{end}}

{{define "list"}}
{{template "layout" .}}
{{define "content"}}
  <h1>SQL View</h1>
  <p>Fichiers SQL disponibles :</p>
  <ul>
  {{- range .Queries }}
    <li><a href="/view/{{ .Name | pathEscape }}">{{ .Title }}</a></li>
  {{- else }}
    <li>Aucun fichier SQL trouvé.</li>
  {{- end }}
  </ul>
  <div class="notice">
    <strong>Note :</strong> chaque fichier SQL doit contenir une requête <code>SELECT</code> ou <code>WITH</code>.
  </div>
{{end}}
{{end}}

{{define "view"}}
{{template "layout" .}}
{{define "content"}}
  <h1>{{ .Title }}</h1>
  <p><a href="/">← Retour à la liste</a></p>
  {{ if .Error }}
    <div class="error">{{ .Error }}</div>
  {{ else }}
    <h2>Requête</h2>
    <pre>{{ .Query }}</pre>
    {{ if .Headers }}
      <table>
        <thead>
          <tr>
            {{ range .Headers }}<th>{{ . }}</th>{{ end }}
          </tr>
        </thead>
        <tbody>
          {{ range .Rows }}
            <tr>
              {{ range . }}<td>{{ . }}</td>{{ end }}
            </tr>
          {{ else }}
            <tr><td colspan="{{ len $.Headers }}">Aucune ligne retournée.</td></tr>
          {{ end }}
        </tbody>
      </table>
    {{ else }}
      <div class="notice">Aucun résultat à afficher.</div>
    {{ end }}
  {{ end }}
{{end}}
{{end}}
`
