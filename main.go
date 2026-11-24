package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	_ "modernc.org/sqlite"
)

type Task struct {
	ID        int64
	Title     string
	Completed bool
}

func (t Task) URL(action string) string {
	return fmt.Sprintf("/todos/%d/%s", t.ID, action)
}

func (t Task) ToggleLabel() string {
	state := "complete"
	if t.Completed {
		state = "active"
	}
	return "Mark " + t.Title + " as " + state
}

type BoardData struct {
	Tasks                                 []Task
	Selected, Token, Error, Draft, Notice string
	Total, Completed                      int
}

func (b BoardData) Active() int { return b.Total - b.Completed }
func (b BoardData) Progress() int {
	if b.Total == 0 {
		return 0
	}
	return (b.Completed*100 + b.Total/2) / b.Total
}

type application struct {
	db  *sql.DB
	key []byte
}

type csrfKey struct{}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	database := os.Getenv("DATABASE")
	if database == "" {
		database = "instance/todos.sqlite"
	}
	if err := os.MkdirAll(filepath.Dir(database), 0700); err != nil {
		return err
	}
	key, err := signingKey(filepath.Join(filepath.Dir(database), "secret.key"))
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS todos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 240),
			completed INTEGER NOT NULL DEFAULT 0 CHECK(completed IN (0, 1)),
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`); err != nil {
		return err
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:5050"
	}
	app := &application{db: db, key: key}
	server := &http.Server{
		Addr: addr, Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("Daylist listening on http://%s", addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

func signingKey(path string) ([]byte, error) {
	if key := os.Getenv("SECRET_KEY"); key != "" {
		if len(key) < 32 {
			return nil, errors.New("SECRET_KEY must contain at least 32 bytes")
		}
		return []byte(key), nil
	}
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = []byte(rand.Text() + rand.Text())
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			return signingKey(path)
		}
		if err != nil {
			return nil, err
		}
		_, writeErr := file.Write(key)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if err != nil {
		return nil, err
	}
	key = bytes.TrimSpace(key)
	if len(key) < 32 {
		return nil, errors.New("session signing key is too short")
	}
	return key, nil
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { a.board(w, r, BoardData{}, http.StatusOK) })
	mux.HandleFunc("POST /todos", a.mutate)
	mux.HandleFunc("POST /todos/{id}/{action}", a.mutate)
	mux.HandleFunc("GET /static/style.css", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "static/style.css") })
	mux.HandleFunc("GET /static/app.js", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "static/app.js") })
	mux.HandleFunc("GET /assets/htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "node_modules/htmx.org/dist/htmx.min.js")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		errorPage(w, r, http.StatusNotFound, "That page no longer exists.")
	})
	return a.protect(mux)
}

func (a *application) signature(token string) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *application) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Cookie, HX-Request, HX-History-Restore-Request")
		var token string
		if cookie, err := r.Cookie("daylist_csrf"); err == nil {
			value, signature, ok := strings.Cut(cookie.Value, ".")
			if ok && len(value) == 64 && hmac.Equal([]byte(signature), []byte(a.signature(value))) {
				token = value
			}
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
			if err := r.ParseForm(); err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					errorPage(w, r, 413, "That request is too large.")
				} else {
					errorPage(w, r, 400, "Invalid form submission.")
				}
				return
			}
			if token == "" || !hmac.Equal([]byte(token), []byte(r.PostForm.Get("csrf_token"))) {
				errorPage(w, r, 400, "Your session expired. Reload the page and try again.")
				return
			}
		}
		if token == "" {
			value := make([]byte, 32)
			if _, err := rand.Read(value); err != nil {
				a.failed(w, r, err)
				return
			}
			token = hex.EncodeToString(value)
			http.SetCookie(w, &http.Cookie{Name: "daylist_csrf", Value: token + "." + a.signature(token), Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfKey{}, token)))
	})
}

func selectedFilter(r *http.Request) string {
	value := r.FormValue("filter")
	if value == "active" || value == "completed" {
		return value
	}
	return "all"
}

func (a *application) board(w http.ResponseWriter, r *http.Request, data BoardData, status int) {
	data.Selected = selectedFilter(r)
	data.Token, _ = r.Context().Value(csrfKey{}).(string)
	rows, err := a.db.QueryContext(r.Context(), "SELECT id, title, completed FROM todos ORDER BY completed, id DESC")
	if err != nil {
		a.failed(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var task Task
		if err := rows.Scan(&task.ID, &task.Title, &task.Completed); err != nil {
			a.failed(w, r, err)
			return
		}
		data.Total++
		if task.Completed {
			data.Completed++
		}
		if data.Selected == "active" && task.Completed || data.Selected == "completed" && !task.Completed {
			continue
		}
		data.Tasks = append(data.Tasks, task)
	}
	if err := rows.Err(); err != nil {
		a.failed(w, r, err)
		return
	}
	component := Board(data)
	if r.Header.Get("HX-Request") != "true" || r.Header.Get("HX-History-Restore-Request") == "true" {
		component = Page(component)
	}
	render(w, r, status, component)
}

func (a *application) mutate(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var id int64
	if action != "" {
		var err error
		id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 || (action != "edit" && action != "toggle" && action != "delete") {
			errorPage(w, r, 404, "That task no longer exists.")
			return
		}
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if action == "" || action == "edit" {
		if !utf8.ValidString(title) || strings.ContainsRune(title, '\x00') || utf8.RuneCountInString(title) < 1 || utf8.RuneCountInString(title) > 240 {
			data := BoardData{Error: "Please enter a task between 1 and 240 characters."}
			if action == "" {
				data.Draft = string([]rune(title)[:min(utf8.RuneCountInString(title), 240)])
			}
			a.board(w, r, data, 422)
			return
		}
	}
	query, args, notice := "INSERT INTO todos (title) VALUES (?)", []any{title}, "Task added"
	switch action {
	case "toggle":
		query, args, notice = "UPDATE todos SET completed = 1 - completed WHERE id = ?", []any{id}, "Task updated"
	case "edit":
		query, args, notice = "UPDATE todos SET title = ? WHERE id = ?", []any{title, id}, "Task saved"
	case "delete":
		query, args, notice = "DELETE FROM todos WHERE id = ?", []any{id}, "Task deleted"
	}
	result, err := a.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		a.failed(w, r, err)
		return
	}
	if count == 0 {
		errorPage(w, r, 404, "That task no longer exists.")
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		a.board(w, r, BoardData{Notice: notice}, 200)
		return
	}
	http.Redirect(w, r, "/?filter="+selectedFilter(r), http.StatusSeeOther)
}

func render(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	var buffer bytes.Buffer
	if err := component.Render(r.Context(), &buffer); err != nil {
		log.Printf("render: %v", err)
		http.Error(w, "Could not render the page.", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buffer.Bytes())
}

func errorPage(w http.ResponseWriter, r *http.Request, status int, message string) {
	render(w, r, status, Page(ErrorView(status, message)))
}

func (a *application) failed(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("request failed: %v", err)
	errorPage(w, r, 500, "That change could not be completed. Please try again.")
}
