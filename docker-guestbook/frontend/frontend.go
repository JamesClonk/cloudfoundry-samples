package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/JamesClonk/vcap"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/unrolled/render"
)

const (
	redisServiceInstance = "redis-docker-guestbook"
)

var env *vcap.VCAP

type Page struct {
	Active     string
	Counter    int64
	Content    interface{}
	Template   string
	StatusCode int
	Error      error
}

type Entry struct {
	Timestamp time.Time
	Text      string
}

type HitCounter struct {
	render *render.Render
}

func (h *HitCounter) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// call increaseHitCounter() on every HTTP request we get, then continue the middleware chain
	if err := increaseHitCounter(); err != nil {
		h.render.HTML(w, http.StatusInternalServerError, "error", Page{Error: err})
		return
	}
	next(w, r)
}

func init() {
	// parse cloudfoundry VCAP_* env data
	var err error
	env, err = vcap.New()
	if err != nil {
		fmt.Printf("ERROR: %v", err)
	}
}

func main() {
	// create render instance
	r := render.New(render.Options{
		IndentJSON: true,
		Layout:     "layout",
		Extensions: []string{".html"},
	})

	// setup routes
	router := mux.NewRouter()
	router.HandleFunc("/", makeHandler("Entries", r, index)).Methods("GET")
	router.HandleFunc("/", makeHandler("Entries", r, newEntry)).Methods("POST")
	router.NotFoundHandler = http.HandlerFunc(makeHandler("", r, notFound))

	// setup negroni
	n := negroni.Classic()
	n.Use(&HitCounter{r}) // make sure HitCounter is first in the middleware chain
	n.UseHandler(router)
	n.Run(fmt.Sprintf(":%d", env.Port))
}

func makeHandler(active string, r *render.Render, fn func(http.ResponseWriter, *http.Request) *Page) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		counter, err := getHitCounter()
		if err != nil {
			r.HTML(w, http.StatusInternalServerError, "error",
				Page{
					Active: active,
					Error:  err,
				},
			)
			return
		}

		page := fn(w, req)
		page.Active = active
		page.Counter = counter

		if page.Error != nil {
			r.HTML(w, page.StatusCode, "error", page)
			return
		}
		r.HTML(w, page.StatusCode, page.Template, page)
	}
}

func index(w http.ResponseWriter, req *http.Request) *Page {
	entries, err := getEntriesFromBackend()
	if err != nil {
		return &Page{
			StatusCode: http.StatusServiceUnavailable,
			Error:      err,
		}
	}
	return &Page{
		StatusCode: http.StatusOK,
		Content:    entries,
		Template:   "index",
	}
}

func newEntry(w http.ResponseWriter, req *http.Request) *Page {
	if err := req.ParseForm(); err != nil {
		return &Page{
			StatusCode: http.StatusInternalServerError,
			Error:      err,
		}
	}

	text := req.FormValue("text")
	if text != "" {
		backend, err := getBackendURL()
		if err != nil {
			return &Page{
				StatusCode: http.StatusInternalServerError,
				Error:      err,
			}
		}

		_, err = http.PostForm(backend+"/entry", url.Values{"text": {text}})
		if err != nil {
			return &Page{
				StatusCode: http.StatusInternalServerError,
				Error:      err,
			}
		}
	}
	http.Redirect(w, req, "/", http.StatusFound)
	return index(w, req)
}

func notFound(w http.ResponseWriter, req *http.Request) *Page {
	return &Page{
		StatusCode: http.StatusNotFound,
		Template:   "404",
	}
}

func getEntriesFromBackend() ([]Entry, error) {
	backend, err := getBackendURL()
	if err != nil {
		return nil, err
	}

	resp, err := http.Get(backend + "/entries")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if strings.Trim(string(body), `\n\r\t `) == "{}" {
		return nil, nil
	}

	var entries []Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("%v: \n[%v]", err.Error(), string(body))
	}
	return entries, nil
}

func getBackendURL() (string, error) {
	endpoint := os.Getenv("BACKEND_ENDPOINT")
	return fmt.Sprintf("http://%s", endpoint), nil
}
