package main

import (
	"net/http"

	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"encoding/json"
	"net/url"
	"io/ioutil"
	"encoding/xml"
	"strconv"

	"github.com/urfave/negroni"
	"github.com/yosssi/ace"
	gmux "github.com/gorilla/mux"
	"gopkg.in/gorp.v1"
	"github.com/goincremental/negroni-sessions"
	"github.com/goincremental/negroni-sessions/cookiestore"
)

type Book struct {
	PK int64 `db:"pk"`
	Title string `db:"title"`
	Author string `db:"author"`
	Classification string `db:"classification"`
	ID string `db:"id"`
}

type Page struct {
	Books []Book
	Filter string
}

type SearchResult struct {
	Title string `xml:"title,attr"`
	Author string `xml:"author,attr"`
	Year string `xml:"hyr,attr"`
	ID int `xml:"owi,attr"`
}

type ClassifyBookResponse struct {
	BookData struct {
		 Title string `xml:"title,attr"`
		 Author string `xml:"author,attr"`
		 ID int `xml:"owi,attr"`
 	} `xml:"work"`
	Classification struct {
		MostPopular string `xml:"sfa,attr"`
	} `xml:"recommendations>ddc>mostPopular"`
}

type ClassifySearchResponse struct {
	Results []SearchResult `xml:"works>work"`
}

var db *sql.DB
var dbmap *gorp.DbMap

func initDB() {
	db, _ = sql.Open("sqlite3", "dev.db")

	dbmap = &gorp.DbMap{Db: db, Dialect: gorp.SqliteDialect{}}
	dbmap.AddTableWithName(Book{}, "books").SetKeys(true, "pk")
	dbmap.CreateTablesIfNotExists()
}

func verifyDatabase(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if err := db.Ping(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	next(w, r)
}

func getBookCollection(books *[]Book, sortCol string, filterByClass string, w http.ResponseWriter) bool {
	if sortCol == "" {
		sortCol = "pk"
	}

	var where string
	if filterByClass == "fiction" {
		where = " WHERE classification BETWEEN '800' and '900'"
	} else if filterByClass == "nonfiction" {
		where = " WHERE classification not BETWEEN '800' and '900'"
	}

	if _, err := dbmap.Select(books, "SELECT * FROM books" + where + " ORDER BY " + sortCol); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}

	return true
}

func getStringFromSession(r *http.Request, key string) string {
	var strVal string
	if val := sessions.GetSession(r).Get(key); val != nil {
		strVal = val.(string)
	}

	return strVal
}

func main() {
	initDB()

	mux := gmux.NewRouter()

	mux.HandleFunc("/books", func(w http.ResponseWriter, r *http.Request) {
		var b []Book
		if !getBookCollection(&b, r.FormValue("sortBy"), r.FormValue("filter"), w) {
			return
		}

		sessions.GetSession(r).Set("Filter", r.FormValue("filter"))

		if err := json.NewEncoder(w).Encode(b); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}).Methods("GET").Queries("filter", "{filter:all|fiction|nonfiction}")

	mux.HandleFunc("/books", func(w http.ResponseWriter, r *http.Request) {
		var b []Book
		if !getBookCollection(&b, getStringFromSession(r, "SortBy"), getStringFromSession(r, "Filter"), w) {
			return
		}

		sessions.GetSession(r).Set("SortBy", r.FormValue("sortBy"))

		if err := json.NewEncoder(w).Encode(b); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}).Methods("GET").Queries("sortBy", "{sortBy:title|author|classification}")

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request){
		//template, err := ace.Load("templates/index", "", nil)
		template, err := ace.Load("templates/index", "", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		//var sortColumn string
		//if sortBy := sessions.GetSession(r).Get("SortBy"); sortBy != nil {
		//	sortColumn = sortBy.(string)
		//}
		p := Page{ Books: []Book{}, Filter: getStringFromSession(r, "Filter") }
		if !getBookCollection(&p.Books, getStringFromSession(r, "SortBy"), getStringFromSession(r, "Filter"), w) {
			return
		}
		//if _, err = dbmap.Select(&p.Books, "select * from books"); err != nil {
		//	http.Error(w, err.Error(), http.StatusInternalServerError)
		//}

		//rows, _ := db.Query("SELECT pk, title, author, classification FROM books")
		//for rows.Next() {
		//	var b Book
		//	rows.Scan(&b.PK, &b.Title, &b.Author, &b.Classification)
		//	p.Books = append(p.Books, b)
		//}

		if err = template.Execute(w, p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("GET")

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request){
		var results []SearchResult
		var err error

		if results, err = search(r.FormValue("search")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		encoder := json.NewEncoder(w)
		if err := encoder.Encode(results); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("POST")

	// Adding books
	mux.HandleFunc("/books", func (w http.ResponseWriter, r *http.Request) {
		var book ClassifyBookResponse
		var err error

		if book, err = find(r.FormValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		b := Book {
			PK: -1,
			Title: book.BookData.Title,
			Author: book.BookData.Author,
			Classification: book.Classification.MostPopular,
		}
		if err = dbmap.Insert(&b); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(b); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}).Methods("PUT")

	// Delete books
	mux.HandleFunc("/books/{pk}", func(w http.ResponseWriter, r *http.Request){
		pk, _ := strconv.ParseInt(gmux.Vars(r)["pk"], 10, 64)
		if _, err := dbmap.Delete(&Book{pk, "", "", "", ""}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}).Methods("DELETE")

	n := negroni.Classic()
	n.Use(sessions.Sessions("go-for-web-dev", cookiestore.New([]byte("my-secret-123"))))
	n.Use(negroni.HandlerFunc(verifyDatabase))
	n.UseHandler(mux)
	n.Run(":8080")
}

func find(id string) (ClassifyBookResponse, error) {
	var c ClassifyBookResponse
	body, err := classifyAPI("http://classify.oclc.org/classify2/Classify?&summary=true&owi=" + url.QueryEscape(id))

	if err != nil {
		return ClassifyBookResponse{}, err
	}

	err = xml.Unmarshal(body, &c)

	return c, err
}

func search(query string) ([]SearchResult, error) {
	var c ClassifySearchResponse
	body, err := classifyAPI("http://classify.oclc.org/classify2/Classify?&summary=true&title=" + url.QueryEscape(query))

	if err != nil {
		return []SearchResult{}, err
	}

	err = xml.Unmarshal(body, &c)

	return c.Results, err
}

func classifyAPI(url string) ([]byte, error) {
	var resp *http.Response
	var err error

	if resp, err = http.Get(url); err != nil {
		return []byte{}, err
	}

	defer resp.Body.Close()

	return ioutil.ReadAll(resp.Body)
}